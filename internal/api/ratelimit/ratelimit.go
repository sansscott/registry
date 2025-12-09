// Package ratelimit provides IP-based rate limiting middleware for HTTP servers.
package ratelimit

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config holds the rate limiting configuration
type Config struct {
	// RequestsPerMinute is the maximum number of requests allowed per minute per IP
	RequestsPerMinute int
	// RequestsPerHour is the maximum number of requests allowed per hour per IP
	RequestsPerHour int
	// CleanupInterval is how often to clean up stale entries (default: 10 minutes)
	CleanupInterval time.Duration
	// SkipPaths are paths that should not be rate limited
	SkipPaths []string
	// TrustProxy determines if X-Forwarded-For and X-Real-IP headers should be trusted.
	// Set to true only when running behind a trusted reverse proxy (e.g., nginx, cloud load balancer).
	// When false, only the direct connection IP (RemoteAddr) is used, preventing IP spoofing.
	TrustProxy bool
	// MaxVisitors is the maximum number of visitor entries to track (memory protection).
	// When exceeded, oldest entries are evicted. Default: 100000.
	MaxVisitors int
}

// DefaultConfig returns the default rate limiting configuration
func DefaultConfig() Config {
	return Config{
		RequestsPerMinute: 60,
		RequestsPerHour:   1000,
		CleanupInterval:   10 * time.Minute,
		SkipPaths:         []string{"/health", "/ping", "/metrics"},
		TrustProxy:        false, // Secure default: don't trust proxy headers
		MaxVisitors:       100000,
	}
}

// visitor tracks rate limiting state for a single IP address
type visitor struct {
	minuteLimiter *rate.Limiter
	hourLimiter   *rate.Limiter
	lastSeen      time.Time
}

// RateLimiter implements IP-based rate limiting
type RateLimiter struct {
	config   Config
	visitors map[string]*visitor
	mu       sync.RWMutex
	stopCh   chan struct{}
}

// New creates a new RateLimiter with the given configuration
func New(cfg Config) *RateLimiter {
	if cfg.MaxVisitors <= 0 {
		cfg.MaxVisitors = 100000
	}

	rl := &RateLimiter{
		config:   cfg,
		visitors: make(map[string]*visitor),
		stopCh:   make(chan struct{}),
	}

	// Start background cleanup goroutine
	go rl.cleanupLoop()

	return rl
}

// Stop stops the background cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// cleanupLoop periodically removes stale visitor entries
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanup removes visitors that haven't been seen in the last hour
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	threshold := time.Now().Add(-time.Hour)
	for ip, v := range rl.visitors {
		if v.lastSeen.Before(threshold) {
			delete(rl.visitors, ip)
		}
	}
}

// evictOldestLocked removes the oldest visitor entry. Must be called with lock held.
func (rl *RateLimiter) evictOldestLocked() {
	var oldestIP string
	var oldestTime time.Time

	for ip, v := range rl.visitors {
		if oldestIP == "" || v.lastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = v.lastSeen
		}
	}

	if oldestIP != "" {
		delete(rl.visitors, oldestIP)
	}
}

// getVisitor returns the visitor for the given IP, creating one if necessary.
// Implements memory protection by evicting oldest entries when MaxVisitors is reached.
func (rl *RateLimiter) getVisitor(ip string) *visitor {
	// Try read lock first for existing visitors (common case)
	rl.mu.RLock()
	v, exists := rl.visitors[ip]
	rl.mu.RUnlock()

	if exists {
		// Update timestamp - this is a minor race but acceptable for lastSeen
		v.lastSeen = time.Now()
		return v
	}

	// Need to create new visitor - acquire write lock
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	v, exists = rl.visitors[ip]
	if exists {
		v.lastSeen = time.Now()
		return v
	}

	// Enforce max visitors limit (memory protection)
	if len(rl.visitors) >= rl.config.MaxVisitors {
		rl.evictOldestLocked()
	}

	// Create rate limiters:
	// - Minute limiter: allows RequestsPerMinute requests per minute with burst of same
	// - Hour limiter: allows RequestsPerHour requests per hour with burst of same
	minuteRate := rate.Limit(float64(rl.config.RequestsPerMinute) / 60.0) // requests per second
	hourRate := rate.Limit(float64(rl.config.RequestsPerHour) / 3600.0)   // requests per second

	v = &visitor{
		minuteLimiter: rate.NewLimiter(minuteRate, rl.config.RequestsPerMinute),
		hourLimiter:   rate.NewLimiter(hourRate, rl.config.RequestsPerHour),
		lastSeen:      time.Now(),
	}
	rl.visitors[ip] = v

	return v
}

// Allow checks if a request from the given IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	v := rl.getVisitor(ip)

	// Both limiters must allow the request
	if !v.minuteLimiter.Allow() {
		return false
	}
	if !v.hourLimiter.Allow() {
		return false
	}
	return true
}

// shouldSkip returns true if the path should not be rate limited
func (rl *RateLimiter) shouldSkip(path string) bool {
	for _, skipPath := range rl.config.SkipPaths {
		if path == skipPath || strings.HasPrefix(path, skipPath+"/") {
			return true
		}
	}
	return false
}

// getClientIP extracts the client IP from the request.
// When TrustProxy is true, it considers X-Forwarded-For and X-Real-IP headers.
// When TrustProxy is false, only RemoteAddr is used to prevent IP spoofing.
func (rl *RateLimiter) getClientIP(r *http.Request) string {
	// Only trust proxy headers if explicitly configured
	if rl.config.TrustProxy {
		// Check X-Forwarded-For header (can contain multiple IPs)
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP (original client)
			if idx := strings.Index(xff, ","); idx != -1 {
				xff = xff[:idx]
			}
			xff = strings.TrimSpace(xff)
			if ip := validateAndNormalizeIP(xff); ip != "" {
				return ip
			}
		}

		// Check X-Real-IP header
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			if ip := validateAndNormalizeIP(strings.TrimSpace(xri)); ip != "" {
				return ip
			}
		}
	}

	// Fall back to RemoteAddr (always used when TrustProxy is false)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port
		ip = r.RemoteAddr
	}

	// Validate and normalize the IP
	if validIP := validateAndNormalizeIP(ip); validIP != "" {
		return validIP
	}

	// If all else fails, use a fallback that won't cause issues
	return "unknown"
}

// validateAndNormalizeIP validates the IP string and returns a normalized form.
// Returns empty string if the IP is invalid.
func validateAndNormalizeIP(ip string) string {
	if ip == "" {
		return ""
	}

	// Parse the IP to validate it
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ""
	}

	// Return normalized string representation
	return parsedIP.String()
}

// Middleware returns an HTTP middleware that enforces rate limiting
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for certain paths
		if rl.shouldSkip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ip := rl.getClientIP(r)

		if !rl.Allow(ip) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)

			errorBody := map[string]interface{}{
				"title":  "Too Many Requests",
				"status": http.StatusTooManyRequests,
				"detail": "Rate limit exceeded. Please reduce request frequency and retry after some time.",
			}

			jsonData, err := json.Marshal(errorBody)
			if err != nil {
				log.Printf("Failed to marshal rate limit error response: %v", err)
				_, _ = w.Write([]byte(`{"title":"Too Many Requests","status":429}`))
				return
			}
			_, _ = w.Write(jsonData)
			return
		}

		next.ServeHTTP(w, r)
	})
}
