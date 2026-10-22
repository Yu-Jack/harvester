package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	harvesterfake "github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
	steveauth "github.com/rancher/steve/pkg/auth"
	steveserver "github.com/rancher/steve/pkg/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/kubernetes/fake"
	authenticationv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
	k8stesting "k8s.io/client-go/testing"
)

func TestKubernetesAuthMiddleware(t *testing.T) {
	identity := authenticationv1.UserInfo{
		Username: "system:serviceaccount:default:reader",
		UID:      "reader-uid",
		Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:default", "system:authenticated"},
		Extra:    map[string]authenticationv1.ExtraValue{"example.io/key": {"value"}},
	}
	authenticated := authenticationv1.TokenReviewStatus{Authenticated: true, User: identity}
	tests := []struct {
		name          string
		authorization string
		cookie        bool
		status        authenticationv1.TokenReviewStatus
		reviewErr     error
		wantReview    bool
		wantStatus    int
	}{
		{name: "missing credentials", wantStatus: http.StatusUnauthorized},
		{name: "session cookie is not a Kubernetes token", cookie: true, wantStatus: http.StatusUnauthorized},
		{name: "unsupported scheme", authorization: "Basic example", wantStatus: http.StatusUnauthorized},
		{name: "empty bearer", authorization: "Bearer ", wantStatus: http.StatusUnauthorized},
		{name: "malformed bearer", authorization: "Bearer example extra", wantStatus: http.StatusUnauthorized},
		{name: "invalid or expired token", authorization: "Bearer example", wantReview: true, wantStatus: http.StatusUnauthorized},
		{name: "TokenReview status error", authorization: "Bearer example", status: authenticationv1.TokenReviewStatus{
			Authenticated: true, User: identity, Error: "token rejected",
		}, wantReview: true, wantStatus: http.StatusUnauthorized},
		{name: "empty authenticated identity", authorization: "Bearer example", status: authenticationv1.TokenReviewStatus{
			Authenticated: true,
		}, wantReview: true, wantStatus: http.StatusUnauthorized},
		{name: "TokenReview unavailable", authorization: "Bearer example", reviewErr: errors.New("API unavailable"), wantReview: true, wantStatus: http.StatusServiceUnavailable},
		{name: "service account identity", authorization: "Bearer example", status: authenticated, wantReview: true, wantStatus: http.StatusNoContent},
		{name: "case insensitive bearer", authorization: "bearer example", status: authenticated, wantReview: true, wantStatus: http.StatusNoContent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			reviewCalls := 0
			client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				reviewCalls++
				review := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
				assert.Equal(t, "example", review.Spec.Token)
				assert.Empty(t, review.Spec.Audiences, "use the Kubernetes API server's default audience")
				review.Status = tc.status
				return true, review, tc.reviewErr
			})

			called := false
			next := steveauth.ExistingContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				info, ok := request.UserFrom(r.Context())
				require.True(t, ok)
				assert.Equal(t, identity.Username, info.GetName())
				assert.Equal(t, identity.UID, info.GetUID())
				assert.Equal(t, identity.Groups, info.GetGroups())
				assert.Equal(t, map[string][]string{"example.io/key": {"value"}}, info.GetExtra())
				assert.NotContains(t, info.GetGroups(), "system:masters")
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("Impersonate-User"))
				assert.Empty(t, r.Header.Get("Impersonate-Group"))
				assert.Empty(t, r.Header.Get("Impersonate-Uid"))
				assert.Empty(t, r.Header.Get("Impersonate-Extra-Username"))
				assert.Empty(t, r.Header["impersonate-extra-example"])
				assert.Equal(t, "preserved", r.Header.Get("X-Request-Id"))
				w.WriteHeader(http.StatusNoContent)
			}))
			handler := kubernetesAuthMiddleware(client.AuthenticationV1().TokenReviews())(next)
			req := httptest.NewRequest(http.MethodGet, "/v1/harvester/harvesterhci.io.settings", nil)
			req.Header.Set("Authorization", tc.authorization)
			req.Header.Set("Impersonate-User", "admin")
			req.Header.Set("Impersonate-Group", "system:masters")
			req.Header.Set("Impersonate-Uid", "admin")
			req.Header.Set("Impersonate-Extra-Username", "admin")
			req.Header["impersonate-extra-example"] = []string{"spoofed"}
			req.Header.Set("X-Request-Id", "preserved")
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: "R_SESS", Value: "example"})
			}
			req = req.WithContext(request.WithUser(req.Context(), &user.DefaultInfo{Name: "untrusted-context"}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)

			assert.Equal(t, tc.wantStatus, response.Code)
			assert.Equal(t, tc.wantStatus == http.StatusNoContent, called)
			if tc.wantReview {
				assert.Equal(t, 1, reviewCalls)
			} else {
				assert.Zero(t, reviewCalls)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, "Bearer", response.Header().Get("WWW-Authenticate"))
			}
			assert.NotContains(t, response.Body.String(), "example")
			assert.NotContains(t, response.Body.String(), "API unavailable")
		})
	}
}

func TestKubernetesAuthMiddlewareReadyz(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		authorization string
		wantStatus    int
		wantReview    bool
	}{
		{name: "existing server token accepted", method: http.MethodGet, path: clusterReadyPath, authorization: "Bearer server-example", wantStatus: http.StatusNoContent},
		{name: "missing server token rejected", method: http.MethodGet, path: clusterReadyPath, wantStatus: http.StatusUnauthorized},
		{name: "wrong server token rejected", method: http.MethodGet, path: clusterReadyPath, authorization: "Bearer wrong-example", wantStatus: http.StatusUnauthorized},
		{name: "query does not bypass server token check", method: http.MethodGet, path: clusterReadyPath + "?test=true", wantStatus: http.StatusUnauthorized},
		{name: "other method requires Kubernetes token", method: http.MethodPost, path: clusterReadyPath, authorization: "Bearer server-example", wantReview: true, wantStatus: http.StatusUnauthorized},
		{name: "child path requires Kubernetes token", method: http.MethodGet, path: clusterReadyPath + "/other", authorization: "Bearer server-example", wantReview: true, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			secretClient := harvesterfake.NewSimpleClientset(createTokenSecret("server-example"))
			called := false
			next := authMiddleware(fakeclients.SecretCache(secretClient.CoreV1().Secrets), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				_, ok := request.UserFrom(r.Context())
				assert.False(t, ok, "readiness must not manufacture an admin identity")
				w.WriteHeader(http.StatusNoContent)
			}))
			handler := kubernetesAuthMiddleware(client.AuthenticationV1().TokenReviews())(next)
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", tc.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)

			assert.Equal(t, tc.wantStatus, response.Code)
			assert.Equal(t, tc.wantStatus == http.StatusNoContent, called)
			if tc.wantReview {
				assert.Len(t, client.Actions(), 1)
			} else {
				assert.Empty(t, client.Actions(), "RKE server tokens must not be sent to TokenReview")
			}
		})
	}
}

type deadlineTokenReviews struct {
	authenticationv1client.TokenReviewInterface
	t *testing.T
}

func (d *deadlineTokenReviews) Create(ctx context.Context, review *authenticationv1.TokenReview, opts metav1.CreateOptions) (*authenticationv1.TokenReview, error) {
	deadline, ok := ctx.Deadline()
	assert.True(d.t, ok)
	assert.WithinDuration(d.t, time.Now().Add(tokenReviewTimeout), deadline, time.Second)
	return d.TokenReviewInterface.Create(ctx, review, opts)
}

func TestKubernetesAuthMiddlewareTimeout(t *testing.T) {
	client := fake.NewSimpleClientset()
	reviews := &deadlineTokenReviews{TokenReviewInterface: client.AuthenticationV1().TokenReviews(), t: t}
	handler := kubernetesAuthMiddleware(reviews)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated request reached handler")
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/harvester", nil)
	req.Header.Set("Authorization", "Bearer example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestKubernetesAuthMiddlewarePreservesAggregation(t *testing.T) {
	client := fake.NewSimpleClientset()
	steve := &steveserver.Server{
		Handler: steveauth.ExistingContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := request.UserFrom(r.Context())
			require.True(t, ok)
			assert.Equal(t, "rancher-user", info.GetName())
			assert.Equal(t, []string{"rancher-group"}, info.GetGroups())
			w.WriteHeader(http.StatusNoContent)
		})),
	}
	server := &HarvesterServer{steve: steve}
	server.Handler = kubernetesAuthMiddleware(client.AuthenticationV1().TokenReviews())(server.steve)
	req := httptest.NewRequest(http.MethodGet, "/v1/harvester", nil)
	req.Header.Set("Impersonate-User", "rancher-user")
	req.Header.Set("Impersonate-Group", "rancher-group")

	direct := httptest.NewRecorder()
	server.Handler.ServeHTTP(direct, req)
	assert.Equal(t, http.StatusUnauthorized, direct.Code)

	// Aggregation wraps Steve itself, not the direct listener's handler.
	aggregation := steveauth.ToMiddleware(steveauth.AuthenticatorFunc(steveauth.Impersonation))(server.steve)
	tunneled := httptest.NewRecorder()
	aggregation.ServeHTTP(tunneled, req)
	assert.Equal(t, http.StatusNoContent, tunneled.Code)
	assert.Empty(t, client.Actions())
}

func TestRedactedAuthenticationHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization":       {"Bearer example"},
		"Proxy-Authorization": {"Basic example"},
		"Cookie":              {"R_SESS=example"},
		"Set-Cookie":          {"R_SESS=example"},
		"X-Auth-Token":        {"example"},
		"X-Api-Key":           {"example"},
		"X-Apikey":            {"example"},
		"X-Client-Secret":     {"example"},
		"x-credential":        {"example"},
		"Impersonate-User":    {"alice"},
		"Accept":              {"application/json"},
		"X-Request-Id":        {"first", "second"},
	}
	original := headers.Clone()
	redacted := redactedAuthenticationHeaders(headers)

	assert.Equal(t, headers, original, "logging must not change authentication inputs")
	assert.Len(t, redacted, len(headers), "all header names must be retained")
	for name := range headers {
		switch name {
		case "Impersonate-User", "Accept", "X-Request-Id":
			assert.Equal(t, headers[name], redacted[name], name)
		default:
			assert.Equal(t, []string{"[REDACTED]"}, redacted[name], name)
		}
	}
	redacted["X-Request-Id"][0] = "changed"
	assert.Equal(t, original, headers, "the logged header map must be an independent copy")
}
