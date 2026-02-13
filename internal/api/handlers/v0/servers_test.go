package v0_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	v0 "github.com/modelcontextprotocol/registry/internal/api/handlers/v0"
	"github.com/modelcontextprotocol/registry/internal/config"
	"github.com/modelcontextprotocol/registry/internal/database"
	"github.com/modelcontextprotocol/registry/internal/service"
	apiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"github.com/modelcontextprotocol/registry/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListServersEndpoint(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), config.NewConfig())

	// Setup test data
	_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/server-alpha",
		Description: "Alpha test server",
		Version:     "1.0.0",
	})
	require.NoError(t, err)

	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/server-beta",
		Description: "Beta test server",
		Version:     "2.0.0",
	})
	require.NoError(t, err)

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		expectedCount  int
		expectedError  string
	}{
		{
			name:           "list all servers",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:           "list with limit",
			queryParams:    "?limit=1",
			expectedStatus: http.StatusOK,
			expectedCount:  1,
		},
		{
			name:           "search servers",
			queryParams:    "?search=alpha",
			expectedStatus: http.StatusOK,
			expectedCount:  1,
		},
		{
			name:           "filter latest only",
			queryParams:    "?version=latest",
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:           "invalid limit",
			queryParams:    "?limit=abc",
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  "validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v0/servers"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp apiv0.ServerListResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Len(t, resp.Servers, tt.expectedCount)
				assert.Equal(t, tt.expectedCount, resp.Metadata.Count)

				// Verify structure
				for _, server := range resp.Servers {
					assert.NotEmpty(t, server.Server.Name)
					assert.NotEmpty(t, server.Server.Description)
					assert.NotNil(t, server.Meta.Official)
				}
			} else if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestGetLatestServerVersionEndpoint(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), config.NewConfig())

	// Setup test data
	_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/detail-server",
		Description: "Server for detail testing",
		Version:     "1.0.0",
	})
	require.NoError(t, err)

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	tests := []struct {
		name           string
		serverName     string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "get existing server latest version",
			serverName:     "com.example/detail-server",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "get non-existent server",
			serverName:     "com.example/non-existent",
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// URL encode the server name
			encodedName := url.PathEscape(tt.serverName)
			req := httptest.NewRequest(http.MethodGet, "/v0/servers/"+encodedName+"/versions/latest", nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp apiv0.ServerResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Equal(t, tt.serverName, resp.Server.Name)
				assert.NotNil(t, resp.Meta.Official)
			} else if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestGetServerVersionEndpoint(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), config.NewConfig())

	serverName := "com.example/version-server"

	// Setup test data with multiple versions
	_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        serverName,
		Description: "Version test server v1",
		Version:     "1.0.0",
	})
	require.NoError(t, err)

	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        serverName,
		Description: "Version test server v2",
		Version:     "2.0.0",
	})
	require.NoError(t, err)

	// Add version with build metadata for URL encoding test
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        serverName,
		Description: "Version test server with build metadata",
		Version:     "1.0.0+20130313144700",
	})
	require.NoError(t, err)

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	tests := []struct {
		name           string
		serverName     string
		version        string
		expectedStatus int
		expectedError  string
		checkResult    func(*testing.T, *apiv0.ServerResponse)
	}{
		{
			name:           "get existing version",
			serverName:     serverName,
			version:        "1.0.0",
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, "1.0.0", resp.Server.Version)
				assert.Equal(t, "Version test server v1", resp.Server.Description)
				assert.False(t, resp.Meta.Official.IsLatest)
			},
		},
		{
			name:           "get latest version",
			serverName:     serverName,
			version:        "2.0.0",
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, "2.0.0", resp.Server.Version)
				assert.True(t, resp.Meta.Official.IsLatest)
			},
		},
		{
			name:           "get non-existent version",
			serverName:     serverName,
			version:        "3.0.0",
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
		{
			name:           "get non-existent server",
			serverName:     "com.example/non-existent",
			version:        "1.0.0",
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
		{
			name:           "get version with build metadata (URL encoded)",
			serverName:     serverName,
			version:        "1.0.0+20130313144700",
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, "1.0.0+20130313144700", resp.Server.Version)
				assert.Equal(t, "Version test server with build metadata", resp.Server.Description)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// URL encode the server name and version
			encodedName := url.PathEscape(tt.serverName)
			encodedVersion := url.PathEscape(tt.version)
			req := httptest.NewRequest(http.MethodGet, "/v0/servers/"+encodedName+"/versions/"+encodedVersion, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp apiv0.ServerResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Equal(t, tt.serverName, resp.Server.Name)
				assert.Equal(t, tt.version, resp.Server.Version)
				assert.NotNil(t, resp.Meta.Official)

				if tt.checkResult != nil {
					tt.checkResult(t, &resp)
				}
			} else if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestGetAllVersionsEndpoint(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), config.NewConfig())

	serverName := "com.example/multi-version-server"

	// Setup test data with multiple versions
	versions := []string{"1.0.0", "1.1.0", "2.0.0"}
	for _, version := range versions {
		_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
			Schema:      model.CurrentSchemaURL,
			Name:        serverName,
			Description: "Multi-version test server " + version,
			Version:     version,
		})
		require.NoError(t, err)
	}

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	tests := []struct {
		name           string
		serverName     string
		expectedStatus int
		expectedCount  int
		expectedError  string
	}{
		{
			name:           "get all versions of existing server",
			serverName:     serverName,
			expectedStatus: http.StatusOK,
			expectedCount:  3,
		},
		{
			name:           "get versions of non-existent server",
			serverName:     "com.example/non-existent",
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// URL encode the server name
			encodedName := url.PathEscape(tt.serverName)
			req := httptest.NewRequest(http.MethodGet, "/v0/servers/"+encodedName+"/versions", nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp apiv0.ServerListResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Len(t, resp.Servers, tt.expectedCount)
				assert.Equal(t, tt.expectedCount, resp.Metadata.Count)

				// Verify all versions are for the same server
				for _, server := range resp.Servers {
					assert.Equal(t, tt.serverName, server.Server.Name)
					assert.NotNil(t, server.Meta.Official)
				}

				// Verify all expected versions are present
				versionSet := make(map[string]bool)
				for _, server := range resp.Servers {
					versionSet[server.Server.Version] = true
				}
				for _, expectedVersion := range versions {
					assert.True(t, versionSet[expectedVersion], "Version %s should be present", expectedVersion)
				}

				// Verify exactly one is marked as latest
				latestCount := 0
				for _, server := range resp.Servers {
					if server.Meta.Official.IsLatest {
						latestCount++
					}
				}
				assert.Equal(t, 1, latestCount, "Exactly one version should be marked as latest")
			} else if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestListServersWithFilter(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), &config.Config{EnableRegistryValidation: false})

	// Setup test data: servers with different distribution types
	// Server with SSE remote
	_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/sse-server",
		Description: "Server with SSE remote",
		Version:     "1.0.0",
		Remotes: []model.Transport{
			{Type: model.TransportTypeSSE, URL: "https://example.com/sse"},
		},
	})
	require.NoError(t, err)

	// Server with streamable-http remote
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/streamable-server",
		Description: "Server with streamable-http remote",
		Version:     "1.0.0",
		Remotes: []model.Transport{
			{Type: model.TransportTypeStreamableHTTP, URL: "https://example.com/mcp"},
		},
	})
	require.NoError(t, err)

	// Server with npm package
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/npm-server",
		Description: "Server with npm package",
		Version:     "1.0.0",
		Packages: []model.Package{
			{
				RegistryType: model.RegistryTypeNPM,
				Identifier:   "@example/mcp-server",
				Version:      "1.0.0",
				Transport:    model.Transport{Type: model.TransportTypeStdio},
			},
		},
	})
	require.NoError(t, err)

	// Server with pypi package
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/pypi-server",
		Description: "Server with pypi package",
		Version:     "1.0.0",
		Packages: []model.Package{
			{
				RegistryType: model.RegistryTypePyPI,
				Identifier:   "example-mcp-server",
				Version:      "1.0.0",
				Transport:    model.Transport{Type: model.TransportTypeStdio},
			},
		},
	})
	require.NoError(t, err)

	// Server with both remote and package (matches multiple filters)
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/multi-server",
		Description: "Server with both SSE and npm",
		Version:     "1.0.0",
		Remotes: []model.Transport{
			{Type: model.TransportTypeSSE, URL: "https://example.com/multi/sse"},
		},
		Packages: []model.Package{
			{
				RegistryType: model.RegistryTypeNPM,
				Identifier:   "@example/multi-server",
				Version:      "1.0.0",
				Transport:    model.Transport{Type: model.TransportTypeStdio},
			},
		},
	})
	require.NoError(t, err)

	// Server with no remotes or packages (should not match any filter)
	_, err = registryService.CreateServer(ctx, &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "com.example/bare-server",
		Description: "Server with no distribution",
		Version:     "1.0.0",
	})
	require.NoError(t, err)

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	tests := []struct {
		name            string
		queryParams     string
		expectedStatus  int
		expectedCount   int
		expectedError   string
		expectedServers []string // server names expected in results
	}{
		{
			name:           "filter by sse",
			queryParams:    "?filter=sse",
			expectedStatus: http.StatusOK,
			expectedCount:  2, // sse-server + multi-server
			expectedServers: []string{
				"com.example/sse-server",
				"com.example/multi-server",
			},
		},
		{
			name:           "filter by streamable-http",
			queryParams:    "?filter=streamable-http",
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			expectedServers: []string{
				"com.example/streamable-server",
			},
		},
		{
			name:           "filter by npm",
			queryParams:    "?filter=npm",
			expectedStatus: http.StatusOK,
			expectedCount:  2, // npm-server + multi-server
			expectedServers: []string{
				"com.example/npm-server",
				"com.example/multi-server",
			},
		},
		{
			name:           "filter by pypi",
			queryParams:    "?filter=pypi",
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			expectedServers: []string{
				"com.example/pypi-server",
			},
		},
		{
			name:           "comma-separated filters (npm,pypi) - OR logic",
			queryParams:    "?filter=npm,pypi",
			expectedStatus: http.StatusOK,
			expectedCount:  3, // npm-server + pypi-server + multi-server
			expectedServers: []string{
				"com.example/npm-server",
				"com.example/pypi-server",
				"com.example/multi-server",
			},
		},
		{
			name:           "comma-separated filters (sse,npm) - OR logic across types",
			queryParams:    "?filter=sse,npm",
			expectedStatus: http.StatusOK,
			expectedCount:  3, // sse-server + npm-server + multi-server (has both)
			expectedServers: []string{
				"com.example/sse-server",
				"com.example/npm-server",
				"com.example/multi-server",
			},
		},
		{
			name:           "invalid filter value",
			queryParams:    "?filter=invalid",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid filter value",
		},
		{
			name:           "partially invalid filter value",
			queryParams:    "?filter=npm,invalid",
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid filter value",
		},
		{
			name:           "filter combined with search",
			queryParams:    "?filter=npm&search=multi",
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			expectedServers: []string{
				"com.example/multi-server",
			},
		},
		{
			name:           "filter combined with version=latest",
			queryParams:    "?filter=sse&version=latest",
			expectedStatus: http.StatusOK,
			expectedCount:  2,
			expectedServers: []string{
				"com.example/sse-server",
				"com.example/multi-server",
			},
		},
		{
			name:           "filter with spaces around values",
			queryParams:    "?filter=npm%2C%20pypi", // "npm, pypi" URL-encoded
			expectedStatus: http.StatusOK,
			expectedCount:  3,
		},
		{
			name:           "filter by oci (no matching servers)",
			queryParams:    "?filter=oci",
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v0/servers"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp apiv0.ServerListResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Len(t, resp.Servers, tt.expectedCount)
				assert.Equal(t, tt.expectedCount, resp.Metadata.Count)

				if tt.expectedServers != nil {
					actualNames := make([]string, len(resp.Servers))
					for i, s := range resp.Servers {
						actualNames[i] = s.Server.Name
					}
					assert.ElementsMatch(t, tt.expectedServers, actualNames)
				}
			} else if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestServersEndpointEdgeCases(t *testing.T) {
	ctx := context.Background()
	registryService := service.NewRegistryService(database.NewTestDB(t), config.NewConfig())

	// Setup test data with edge case names that comply with constraints
	specialServers := []struct {
		name        string
		description string
		version     string
	}{
		{"io.dots.and-dashes/server-name", "Server with dots and dashes", "1.0.0"},
		{"com.long-namespace-name/very-long-server-name-here", "Long names", "1.0.0"},
		{"org.test123/server_with_underscores", "Server with underscores", "1.0.0"},
	}

	for _, server := range specialServers {
		_, err := registryService.CreateServer(ctx, &apiv0.ServerJSON{
			Schema:      model.CurrentSchemaURL,
			Name:        server.name,
			Description: server.description,
			Version:     server.version,
		})
		require.NoError(t, err)
	}

	// Create API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterServersEndpoints(api, "/v0", registryService)

	t.Run("URL encoding edge cases", func(t *testing.T) {
		tests := []struct {
			name       string
			serverName string
		}{
			{"dots and dashes", "io.dots.and-dashes/server-name"},
			{"long server name", "com.long-namespace-name/very-long-server-name-here"},
			{"underscores", "org.test123/server_with_underscores"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Test latest version endpoint
				encodedName := url.PathEscape(tt.serverName)
				req := httptest.NewRequest(http.MethodGet, "/v0/servers/"+encodedName+"/versions/latest", nil)
				w := httptest.NewRecorder()

				mux.ServeHTTP(w, req)

				assert.Equal(t, http.StatusOK, w.Code)

				var resp apiv0.ServerResponse
				err := json.NewDecoder(w.Body).Decode(&resp)
				assert.NoError(t, err)
				assert.Equal(t, tt.serverName, resp.Server.Name)
			})
		}
	})

	t.Run("query parameter edge cases", func(t *testing.T) {
		tests := []struct {
			name           string
			queryParams    string
			expectedStatus int
			expectedError  string
		}{
			{"limit too high", "?limit=1000", http.StatusUnprocessableEntity, "validation failed"},
			{"negative limit", "?limit=-1", http.StatusUnprocessableEntity, "validation failed"},
			{"invalid updated_since format", "?updated_since=invalid", http.StatusBadRequest, "Invalid updated_since format"},
			{"future updated_since", "?updated_since=2030-01-01T00:00:00Z", http.StatusOK, ""},
			{"very old updated_since", "?updated_since=1990-01-01T00:00:00Z", http.StatusOK, ""},
			{"empty search parameter", "?search=", http.StatusOK, ""},
			{"search with special characters", "?search=测试", http.StatusOK, ""},
			{"combined valid parameters", "?search=server&limit=5&version=latest", http.StatusOK, ""},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/v0/servers"+tt.queryParams, nil)
				w := httptest.NewRecorder()

				mux.ServeHTTP(w, req)

				assert.Equal(t, tt.expectedStatus, w.Code)

				if tt.expectedStatus == http.StatusOK {
					var resp apiv0.ServerListResponse
					err := json.NewDecoder(w.Body).Decode(&resp)
					assert.NoError(t, err)
					assert.NotNil(t, resp.Metadata)
				} else if tt.expectedError != "" {
					assert.Contains(t, w.Body.String(), tt.expectedError)
				}
			})
		}
	})

	t.Run("response structure validation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/servers", nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var resp apiv0.ServerListResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		assert.NoError(t, err)

		// Verify metadata structure
		assert.NotNil(t, resp.Metadata)
		assert.GreaterOrEqual(t, resp.Metadata.Count, 0)

		// Verify each server has complete structure
		for _, server := range resp.Servers {
			assert.NotEmpty(t, server.Server.Name)
			assert.NotEmpty(t, server.Server.Description)
			assert.NotEmpty(t, server.Server.Version)
			assert.NotNil(t, server.Meta)
			assert.NotNil(t, server.Meta.Official)
			assert.NotZero(t, server.Meta.Official.PublishedAt)
			assert.Contains(t, []model.Status{model.StatusActive, model.StatusDeprecated, model.StatusDeleted}, server.Meta.Official.Status)
		}
	})
}
