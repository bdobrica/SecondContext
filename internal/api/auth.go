package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/models"
)

const (
	authErrorType                 = "authentication_error"
	authRequiredErrorCode         = "authentication_required"
	invalidAuthTokenErrorCode     = "invalid_authentication_token"
	authorizationErrorType        = "authorization_error"
	authorizationHeaderName       = "Authorization"
	wwwAuthenticateHeaderName     = "WWW-Authenticate"
	wwwAuthenticateSchemeBearer   = "Bearer"
	defaultAuthenticatedUserLabel = "authenticated-user"
)

type authContextKey struct{}

type authPrincipal struct {
	Subject string
}

type requestScopeError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	Param      string
}

func (e *requestScopeError) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

type requestAuthenticator struct {
	realm  string
	tokens []config.AuthTokenConfig
}

func newRequestAuthenticator(cfg config.AuthConfig) *requestAuthenticator {
	if !cfg.Enabled {
		return nil
	}

	return &requestAuthenticator{realm: strings.TrimSpace(cfg.Realm), tokens: append([]config.AuthTokenConfig(nil), cfg.Tokens...)}
}

func (a *requestAuthenticator) middleware(server *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipAuthentication(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			token, errCode, message := bearerTokenFromRequest(r)
			if strings.TrimSpace(token) == "" {
				a.writeAuthenticationChallenge(w)
				server.writeAPIError(w, r, http.StatusUnauthorized, message, authErrorType, errCode, "")
				return
			}

			principal, ok := a.authenticate(token)
			if !ok {
				a.writeAuthenticationChallenge(w)
				server.writeAPIError(w, r, http.StatusUnauthorized, "invalid bearer token", authErrorType, invalidAuthTokenErrorCode, "")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, principal)))
		})
	}
}

func (a *requestAuthenticator) authenticate(token string) (authPrincipal, bool) {
	for _, candidate := range a.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(candidate.Token)) != 1 {
			continue
		}

		return authPrincipal{Subject: strings.TrimSpace(candidate.Subject)}, true
	}

	return authPrincipal{}, false
}

func (a *requestAuthenticator) writeAuthenticationChallenge(w http.ResponseWriter) {
	realm := strings.TrimSpace(a.realm)
	if realm == "" {
		realm = "second-context"
	}

	w.Header().Set(wwwAuthenticateHeaderName, fmt.Sprintf(`%s realm=%q`, wwwAuthenticateSchemeBearer, realm))
}

func shouldSkipAuthentication(path string) bool {
	return path == "/healthz"
}

func bearerTokenFromRequest(r *http.Request) (string, string, string) {
	header := strings.TrimSpace(r.Header.Get(authorizationHeaderName))
	if header == "" {
		return "", authRequiredErrorCode, "missing Authorization header"
	}

	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(strings.TrimSpace(scheme), wwwAuthenticateSchemeBearer) || strings.TrimSpace(token) == "" {
		return "", authRequiredErrorCode, "Authorization header must use Bearer token authentication"
	}

	return strings.TrimSpace(token), "", ""
}

func authenticatedPrincipal(ctx context.Context) (authPrincipal, bool) {
	principal, ok := ctx.Value(authContextKey{}).(authPrincipal)
	if !ok {
		return authPrincipal{}, false
	}
	if strings.TrimSpace(principal.Subject) == "" {
		return principal, true
	}
	return principal, true
}

func authenticatedSubject(ctx context.Context) string {
	principal, ok := authenticatedPrincipal(ctx)
	if !ok {
		return ""
	}

	return strings.TrimSpace(principal.Subject)
}

func (s *Server) defaultUserExternalID(ctx context.Context, values ...string) string {
	if subject := authenticatedSubject(ctx); subject != "" {
		return subject
	}
	if resolved := firstNonEmpty(values...); strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return s.cfg.Dev.UserExternalID
}

func (s *Server) resolveRequestMetadata(ctx context.Context, metadataValues map[string]any, requestUser string) requestMetadata {
	metadata := parseRequestMetadata(metadataValues)
	metadata.UserExternalID = s.defaultUserExternalID(ctx, metadata.UserExternalID, strings.TrimSpace(requestUser))
	if metadata.UserName == "" {
		if metadata.UserExternalID == s.cfg.Dev.UserExternalID {
			metadata.UserName = s.cfg.Dev.UserName
		} else {
			metadata.UserName = firstNonEmpty(authenticatedSubject(ctx), metadata.UserExternalID, defaultAuthenticatedUserLabel)
		}
	}
	if metadata.UserEmail == "" && metadata.UserExternalID == s.cfg.Dev.UserExternalID {
		metadata.UserEmail = s.cfg.Dev.UserEmail
	}

	return metadata
}

func (s *Server) actorUser(ctx context.Context) (models.User, bool, error) {
	subject := authenticatedSubject(ctx)
	if subject == "" || s.dbPool == nil {
		return models.User{}, false, nil
	}

	resolvedName := subject
	resolvedEmail := ""
	if subject == s.cfg.Dev.UserExternalID {
		resolvedName = s.cfg.Dev.UserName
		resolvedEmail = s.cfg.Dev.UserEmail
	}

	user, err := db.NewUserRepository(s.dbPool).Ensure(ctx, db.EnsureUserParams{
		ExternalID:  subject,
		Email:       resolvedEmail,
		DisplayName: resolvedName,
	})
	if err != nil {
		return models.User{}, false, err
	}

	return user, true, nil
}

func (s *Server) ensureActorOwnsUserID(ctx context.Context, resourceUserID, message, code, param string) error {
	actor, ok, err := s.actorUser(ctx)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(resourceUserID) == "" {
		return nil
	}
	if actor.ID == strings.TrimSpace(resourceUserID) {
		return nil
	}

	return &requestScopeError{
		StatusCode: http.StatusNotFound,
		Message:    message,
		Type:       "invalid_request_error",
		Code:       code,
		Param:      param,
	}
}

func (s *Server) writeRequestScopeError(w http.ResponseWriter, r *http.Request, err error) bool {
	var scopeErr *requestScopeError
	if !errors.As(err, &scopeErr) {
		return false
	}

	s.writeAPIError(w, r, scopeErr.StatusCode, scopeErr.Message, scopeErr.Type, scopeErr.Code, scopeErr.Param)
	return true
}
