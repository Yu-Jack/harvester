package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	steveauth "github.com/rancher/steve/pkg/auth"
	"github.com/sirupsen/logrus"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	authenticationv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
)

const (
	tokenReviewTimeout = 10 * time.Second
	clusterReadyPath   = "/v1/harvester/readyz"
)

func kubernetesAuthMiddleware(reviews authenticationv1client.TokenReviewInterface) steveauth.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logrus.WithFields(logrus.Fields{
				"method":  r.Method,
				"path":    r.URL.Path,
				"host":    r.Host,
				"headers": redactedAuthenticationHeaders(r.Header),
			}).Info("Received direct API request before authentication")

			// This route authenticates the RKE server token in its own middleware.
			if r.Method == http.MethodGet && r.URL.Path == clusterReadyPath {
				logrus.WithField("path", r.URL.Path).Info("Skipping TokenReview for cluster ready path")
				next.ServeHTTP(w, r)
				return
			}

			authorization := strings.Fields(r.Header.Get("Authorization"))
			if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
				logrus.WithFields(logrus.Fields{
					"method":      r.Method,
					"path":        r.URL.Path,
					"parts":       len(authorization),
					"hasAuthzHdr": r.Header.Get("Authorization") != "",
				}).Warn("Missing or malformed Authorization header, expected 'Bearer <token>'")
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			logrus.WithFields(logrus.Fields{
				"method":  r.Method,
				"path":    r.URL.Path,
				"timeout": tokenReviewTimeout,
			}).Info("Sending Kubernetes TokenReview")
			ctx, cancel := context.WithTimeout(r.Context(), tokenReviewTimeout)
			defer cancel()
			review, err := reviews.Create(ctx, &authenticationv1.TokenReview{
				Spec: authenticationv1.TokenReviewSpec{
					Token: authorization[1],
				},
			}, metav1.CreateOptions{})
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"method": r.Method,
					"path":   r.URL.Path,
				}).Error("Failed to authenticate request with Kubernetes TokenReview")
				http.Error(w, "Authentication service unavailable", http.StatusServiceUnavailable)
				return
			}
			if !review.Status.Authenticated || review.Status.Error != "" || review.Status.User.Username == "" {
				logrus.WithFields(logrus.Fields{
					"authenticated": review.Status.Authenticated,
					"error":         review.Status.Error,
					"username":      review.Status.User.Username,
					"method":        r.Method,
					"path":          r.URL.Path,
					"audiences":     review.Status.Audiences,
				}).Error("Kubernetes TokenReview rejected the request")
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			info := &user.DefaultInfo{
				Name:   review.Status.User.Username,
				UID:    review.Status.User.UID,
				Groups: review.Status.User.Groups,
				Extra:  make(map[string][]string, len(review.Status.User.Extra)),
			}
			for key, values := range review.Status.User.Extra {
				info.Extra[key] = values
			}

			logrus.WithFields(logrus.Fields{
				"method":    r.Method,
				"path":      r.URL.Path,
				"username":  info.Name,
				"uid":       info.UID,
				"groups":    info.Groups,
				"extraKeys": len(info.Extra),
			}).Info("Kubernetes TokenReview authenticated the request")

			// Only aggregation may supply trusted impersonation headers.
			r = r.Clone(request.WithUser(r.Context(), info))
			for key := range r.Header {
				if strings.HasPrefix(strings.ToLower(key), "impersonate-") {
					logrus.WithField("header", key).Warn("Dropping client-supplied impersonation header")
					delete(r.Header, key)
				}
			}

			r.Header.Del("Authorization")
			next.ServeHTTP(w, r)
		})
	}
}

// denyAllAuthMiddleware rejects every request except the readyz path; temporary, for verification.
func denyAllAuthMiddleware() steveauth.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == clusterReadyPath {
				next.ServeHTTP(w, r)
				return
			}

			logrus.WithFields(logrus.Fields{
				"method":  r.Method,
				"path":    r.URL.Path,
				"host":    r.Host,
				"headers": redactedAuthenticationHeaders(r.Header),
			}).Warn("Denying request: deny-all middleware is active")
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

func redactedAuthenticationHeaders(headers http.Header) http.Header {
	redacted := headers.Clone()
	for name := range redacted {
		lowerName := strings.ToLower(name)
		for _, sensitive := range []string{"authorization", "cookie", "token", "secret", "api-key", "apikey", "credential"} {
			if strings.Contains(lowerName, sensitive) {
				redacted[name] = []string{"[REDACTED]"}
				break
			}
		}
	}
	return redacted
}
