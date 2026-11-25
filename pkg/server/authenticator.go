package server

import (
	"fmt"
	"net/http"
	"strings"

	steveauth "github.com/rancher/steve/pkg/auth"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
)

// tokenImpersonationAuthenticator authenticates direct requests with the bearer token via TokenReview.
// When Impersonate-* headers are present, the authenticated requester must be allowed to impersonate
// the requested user, groups and extras, which is the same rule kube-apiserver applies.
type tokenImpersonationAuthenticator struct {
	client kubernetes.Interface
}

func newTokenImpersonationAuthenticator(client kubernetes.Interface) steveauth.Authenticator {
	return &tokenImpersonationAuthenticator{client: client}
}

func (a *tokenImpersonationAuthenticator) Authenticate(req *http.Request) (user.Info, bool, error) {
	token := bearerToken(req)
	if token == "" {
		return nil, false, nil
	}

	requester, ok, err := a.reviewToken(req, token)
	if err != nil || !ok {
		return nil, false, err
	}

	impersonated, ok, err := steveauth.Impersonation(req)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return requester, true, nil
	}

	allowed, err := a.canImpersonate(req, requester, impersonated)
	if err != nil || !allowed {
		return nil, false, err
	}

	return withAuthenticatedGroup(impersonated), true, nil
}

func bearerToken(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func (a *tokenImpersonationAuthenticator) reviewToken(req *http.Request, token string) (user.Info, bool, error) {
	review, err := a.client.AuthenticationV1().TokenReviews().Create(req.Context(), &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{Token: token},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("failed to review token: %w", err)
	}
	if !review.Status.Authenticated {
		return nil, false, nil
	}

	extra := make(map[string][]string, len(review.Status.User.Extra))
	for k, v := range review.Status.User.Extra {
		extra[k] = v
	}
	return &user.DefaultInfo{
		Name:   review.Status.User.Username,
		UID:    review.Status.User.UID,
		Groups: review.Status.User.Groups,
		Extra:  extra,
	}, true, nil
}

func (a *tokenImpersonationAuthenticator) canImpersonate(req *http.Request, requester, target user.Info) (bool, error) {
	var attrs []authorizationv1.ResourceAttributes

	if namespace, name, err := serviceaccount.SplitUsername(target.GetName()); err == nil {
		attrs = append(attrs, authorizationv1.ResourceAttributes{
			Verb: "impersonate", Resource: "serviceaccounts", Namespace: namespace, Name: name,
		})
	} else {
		attrs = append(attrs, authorizationv1.ResourceAttributes{
			Verb: "impersonate", Resource: "users", Name: target.GetName(),
		})
	}

	for _, group := range target.GetGroups() {
		attrs = append(attrs, authorizationv1.ResourceAttributes{
			Verb: "impersonate", Resource: "groups", Name: group,
		})
	}

	for key, values := range target.GetExtra() {
		for _, value := range values {
			attrs = append(attrs, authorizationv1.ResourceAttributes{
				Verb: "impersonate", Group: "authentication.k8s.io", Resource: "userextras",
				Subresource: key, Name: value,
			})
		}
	}

	extra := make(map[string]authorizationv1.ExtraValue, len(requester.GetExtra()))
	for k, v := range requester.GetExtra() {
		extra[k] = v
	}

	for i := range attrs {
		sar, err := a.client.AuthorizationV1().SubjectAccessReviews().Create(req.Context(), &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				ResourceAttributes: &attrs[i],
				User:               requester.GetName(),
				UID:                requester.GetUID(),
				Groups:             requester.GetGroups(),
				Extra:              extra,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to check impersonate permission: %w", err)
		}
		if !sar.Status.Allowed {
			return false, nil
		}
	}

	return true, nil
}

// withAuthenticatedGroup mirrors kube-apiserver, which adds system:authenticated to impersonated non-anonymous users.
func withAuthenticatedGroup(info user.Info) user.Info {
	if info.GetName() == user.Anonymous {
		return info
	}
	groups := info.GetGroups()
	for _, group := range groups {
		if group == user.AllAuthenticated {
			return info
		}
	}
	return &user.DefaultInfo{
		Name:   info.GetName(),
		UID:    info.GetUID(),
		Groups: append(append([]string{}, groups...), user.AllAuthenticated),
		Extra:  info.GetExtra(),
	}
}
