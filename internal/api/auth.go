package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
			startedAt := time.Now()
			if shouldSkipAuthentication(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			token, errCode, message := bearerTokenFromRequest(r)
			if strings.TrimSpace(token) == "" {
				a.writeAuthenticationChallenge(w)
				server.observeRejectedRequest(r, http.StatusUnauthorized, time.Since(startedAt), errCode)
				server.writeAPIError(w, r, http.StatusUnauthorized, message, authErrorType, errCode, "")
				return
			}

			principal, ok := a.authenticate(token)
			if !ok || strings.TrimSpace(principal.Subject) == "" {
				a.writeAuthenticationChallenge(w)
				server.observeRejectedRequest(r, http.StatusUnauthorized, time.Since(startedAt), invalidAuthTokenErrorCode)
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

		subject := strings.TrimSpace(candidate.Subject)
		if subject == "" {
			return authPrincipal{}, false
		}
		return authPrincipal{Subject: subject}, true
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
		return authPrincipal{}, false
	}
	principal.Subject = strings.TrimSpace(principal.Subject)
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

func (s *Server) resolveUserExternalID(ctx context.Context, selectors ...requestUserSelector) (string, error) {
	if subject := authenticatedSubject(ctx); subject != "" {
		for _, selector := range selectors {
			value := strings.TrimSpace(selector.Value)
			if value != "" && value != subject {
				return "", &requestScopeError{
					StatusCode: http.StatusBadRequest,
					Message:    selector.Param + " must match the authenticated subject",
					Type:       "invalid_request_error",
					Code:       "identity_conflict",
					Param:      selector.Param,
				}
			}
		}
		return subject, nil
	}

	values := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		values = append(values, selector.Value)
	}
	return s.defaultUserExternalID(ctx, values...), nil
}

type requestUserSelector struct {
	Param string
	Value string
}

func (s *Server) resolveRequestMetadata(ctx context.Context, metadataValues map[string]any, requestUser string) (requestMetadata, error) {
	metadata := parseRequestMetadata(metadataValues)
	userExternalID, err := s.resolveUserExternalID(ctx,
		requestUserSelector{Param: "metadata.user_external_id", Value: metadata.UserExternalID},
		requestUserSelector{Param: "user", Value: requestUser},
	)
	if err != nil {
		return requestMetadata{}, err
	}
	metadata.UserExternalID = userExternalID
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

	return metadata, nil
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
