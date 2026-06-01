package api

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/lich0821/ccNexus/internal/logger"
)

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// SuccessResponse represents a generic success response
type SuccessResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Message string      `json:"message,omitempty"`
}

// WriteJSON writes a JSON response
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response: %v", err)
	}
}

// WriteError writes an error response
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, ErrorResponse{Error: message})
}

// WriteSuccess writes a success response
func WriteSuccess(w http.ResponseWriter, data interface{}) {
	WriteJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Data:    data,
	})
}

// CORSMiddleware adds CORS headers, reflecting only loopback origins so a
// remote website cannot make cross-origin requests to this local admin API.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isLoopbackOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isLoopbackOrigin reports whether an Origin header points at a loopback host.
func isLoopbackOrigin(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RecoveryMiddleware recovers from panics and returns 500 error
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("Panic recovered: %v", err)
				WriteError(w, http.StatusInternalServerError, "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("[API] %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

type AuthConfig struct {
	Enabled  bool
	Username string
	Password string
}

func BasicAuthMiddleware(auth AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			const prefix = "Basic "
			if !strings.HasPrefix(authHeader, prefix) {
				w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			encoded := strings.TrimPrefix(authHeader, prefix)
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			credentials := string(decoded)
			colonIndex := strings.Index(credentials, ":")
			if colonIndex < 0 {
				w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			username := credentials[:colonIndex]
			password := credentials[colonIndex+1:]

			if subtle.ConstantTimeCompare([]byte(auth.Username), []byte(username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(auth.Password), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
