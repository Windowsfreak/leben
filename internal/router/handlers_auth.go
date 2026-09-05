package router

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/models"
)

// clientIP resolves the caller IP, honoring the reverse-proxy headers set by
// Caddy in production.
func clientIP(req *http.Request) string {
	if fwd := req.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.Index(fwd, ","); i > 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	if rip := req.Header.Get("X-Real-IP"); rip != "" {
		return rip
	}
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

// publicBaseURL builds absolute URLs for verification links. Prefer the
// configured public_url, fall back to proxy headers / request host.
func (r *Router) publicBaseURL(req *http.Request) string {
	if r.cfg.Server.PublicURL != "" {
		return strings.TrimRight(r.cfg.Server.PublicURL, "/")
	}
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	} else if proto := req.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + req.Host
}

// handleDeviceStart implements RFC 8628 §3.1-3.2: the device asks for a
// device code + user code and gets the URL the human must open.
func (r *Router) handleDeviceStart(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ip := clientIP(req)
	if !r.auth.AllowRate("device-start", ip, 10, 10) {
		writeError(w, http.StatusTooManyRequests, "Too many requests. Slow down.")
		return
	}

	grant, err := r.auth.CreateDeviceGrant()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	base := r.publicBaseURL(req)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               grant.DeviceCode,
		"user_code":                 grant.UserCode,
		"verification_uri":          base + "/device.html",
		"verification_uri_complete": base + "/device.html?code=" + grant.UserCode,
		"expires_in":                grant.ExpiresIn,
		"interval":                  2,
	})
}

// handleDeviceToken is the RFC 8628 §3.4 polling endpoint. Accepts JSON (used
// by our MCP server) or form-encoded bodies (RFC style).
func (r *Router) handleDeviceToken(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ip := clientIP(req)
	if !r.auth.AllowRate("device-token", ip, 60, 10) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "slow_down"})
		return
	}

	var grantType, deviceCode string
	if strings.Contains(req.Header.Get("Content-Type"), "application/json") {
		var body struct {
			GrantType  string `json:"grant_type"`
			DeviceCode string `json:"device_code"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		grantType, deviceCode = body.GrantType, body.DeviceCode
	} else {
		_ = req.ParseForm()
		grantType = req.PostFormValue("grant_type")
		deviceCode = req.PostFormValue("device_code")
	}

	if deviceCode == "" || (grantType != "" && grantType != "urn:ietf:params:oauth:grant-type:device_code") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
		return
	}

	token, err := r.auth.ConsumeDeviceGrant(deviceCode, req.UserAgent(), ip)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
		})
	case errors.Is(err, auth.ErrDevicePending):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "authorization_pending"})
	case errors.Is(err, auth.ErrDeviceDenied):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "access_denied"})
	case errors.Is(err, auth.ErrDeviceUsed), errors.Is(err, auth.ErrDeviceUnknown):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expired_token"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
	}
}

// handleDeviceAuthorize is called by the browser approval page AFTER the
// admin entered the user code and their password. The password travels only
// browser → server, never to the requesting device.
func (r *Router) handleDeviceAuthorize(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ip := clientIP(req)
	if !r.auth.AllowRate("device-authorize", ip, 5, 5) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a minute and try again.")
		return
	}

	var body struct {
		UserCode string `json:"user_code"`
		Password string `json:"password"`
		Approve  *bool  `json:"approve"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body.")
		return
	}

	if !r.auth.VerifyPassword(body.Password) {
		writeError(w, http.StatusUnauthorized, "Invalid admin password.")
		return
	}

	userCode := strings.ToUpper(strings.TrimSpace(body.UserCode))
	approve := body.Approve != nil && *body.Approve
	if err := r.auth.AuthorizeDeviceGrant(userCode, approve); err != nil {
		if errors.Is(err, auth.ErrNoPendingGrant) {
			writeError(w, http.StatusNotFound, "No pending authorization request for this code. It may have expired — start the login again.")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	message := "Request approved. The device will receive its token within its polling interval."
	if !approve {
		message = "Request denied."
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": message})
}

// handleAdminListTokens lists all issued credentials — browser sessions and
// device tokens — marking the caller's own so the UI can warn on self-revocation.
func (r *Router) handleAdminListTokens(w http.ResponseWriter, req *http.Request) {
	tokens, err := r.auth.ListAPITokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	currentID, _ := r.auth.LookupTokenID(auth.ExtractToken(req))
	views := make([]models.ApiTokenView, 0, len(tokens))
	for _, t := range tokens {
		views = append(views, models.ApiTokenView{ApiToken: t, Current: t.ID == currentID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "tokens": views})
}

// handleAdminRevokeToken immediately invalidates an API token.
func (r *Router) handleAdminRevokeToken(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	id, err := strconv.Atoi(ps.ByName("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid token ID.")
		return
	}
	if err := r.auth.RevokeAPIToken(id); err != nil {
		writeError(w, http.StatusNotFound, "Token not found or already revoked.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "Token revoked."})
}

// handleHealthz is a public liveness/readiness probe.
func (r *Router) handleHealthz(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	code := http.StatusOK
	if r.db == nil || r.db.PingContext(ctx) != nil {
		dbStatus = "error"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"status": map[bool]string{true: "ok", false: "degraded"}[dbStatus == "ok"],
		"db":     dbStatus,
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handleAdminGetConfig exposes the frontend config.json (previously write-only).
func (r *Router) handleAdminGetConfig(w http.ResponseWriter, req *http.Request) {
	data, err := os.ReadFile(filepath.Join(r.cfg.Server.WebDir, "config.json"))
	if err != nil {
		writeError(w, http.StatusNotFound, "config.json not found.")
		return
	}
	var js json.RawMessage
	if err := json.Unmarshal(data, &js); err != nil {
		writeError(w, http.StatusInternalServerError, "config.json is not valid JSON.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "config": js})
}

// handleOAuthProtectedResource implements RFC 9728 OAuth 2.0 Protected Resource Metadata
func (r *Router) handleOAuthProtectedResource(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	base := r.publicBaseURL(req)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource": base + "/api/mcp",
		"authorization_servers": []string{
			base,
		},
		"scopes_supported": []string{
			"admin",
			"read",
		},
		"bearer_methods_supported": []string{
			"header",
		},
		"resource_documentation": base + "/llms-admin.txt",
	})
}

// handleOAuthAuthServer implements RFC 8414 OAuth 2.0 Authorization Server Metadata
func (r *Router) handleOAuthAuthServer(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	base := r.publicBaseURL(req)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"device_authorization_endpoint":         base + "/api/auth/device",
		"token_endpoint":                        base + "/api/auth/device/token",
		"token_endpoint_auth_methods_supported": []string{"none"},
		"grant_types_supported": []string{
			"urn:ietf:params:oauth:grant-type:device_code",
		},
		"response_types_supported": []string{},
		"service_documentation":    base + "/llms-admin.txt",
	})
}

