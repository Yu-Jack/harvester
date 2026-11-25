package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestTokenImpersonationAuthenticator(t *testing.T) {
	const validToken = "valid-token"
	requester := authenticationv1.UserInfo{
		Username: "system:serviceaccount:cattle-system:rancher",
		Groups:   []string{"system:serviceaccounts", "system:authenticated"},
	}

	tests := []struct {
		name            string
		headers         map[string][]string
		allowedResource map[string]bool
		expectedOK      bool
		expectedUser    string
		expectedGroups  []string
		expectedSARs    int
	}{
		{
			name:       "no bearer token",
			headers:    map[string][]string{"Impersonate-User": {"admin"}},
			expectedOK: false,
		},
		{
			name:       "invalid bearer token",
			headers:    map[string][]string{"Authorization": {"Bearer bad-token"}},
			expectedOK: false,
		},
		{
			name:           "valid token without impersonation",
			headers:        map[string][]string{"Authorization": {"Bearer " + validToken}},
			expectedOK:     true,
			expectedUser:   requester.Username,
			expectedGroups: requester.Groups,
		},
		{
			name: "impersonation allowed",
			headers: map[string][]string{
				"Authorization":              {"Bearer " + validToken},
				"Impersonate-User":           {"user-abc"},
				"Impersonate-Group":          {"system:cattle:authenticated"},
				"Impersonate-Extra-Username": {"admin"},
			},
			allowedResource: map[string]bool{"users": true, "groups": true, "userextras": true},
			expectedOK:      true,
			expectedUser:    "user-abc",
			expectedGroups:  []string{"system:cattle:authenticated", user.AllAuthenticated},
			expectedSARs:    3,
		},
		{
			name: "impersonating group denied",
			headers: map[string][]string{
				"Authorization":     {"Bearer " + validToken},
				"Impersonate-User":  {"admin"},
				"Impersonate-Group": {"system:masters"},
			},
			allowedResource: map[string]bool{"users": true},
			expectedOK:      false,
			expectedSARs:    2,
		},
		{
			name: "impersonating service account checks serviceaccounts resource",
			headers: map[string][]string{
				"Authorization":    {"Bearer " + validToken},
				"Impersonate-User": {"system:serviceaccount:default:foo"},
			},
			allowedResource: map[string]bool{"serviceaccounts": true},
			expectedOK:      true,
			expectedUser:    "system:serviceaccount:default:foo",
			expectedGroups:  []string{user.AllAuthenticated},
			expectedSARs:    1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				review := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
				if review.Spec.Token == validToken {
					review.Status.Authenticated = true
					review.Status.User = requester
				}
				return true, review, nil
			})
			sarCount := 0
			client.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				sarCount++
				sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
				assert.Equal(t, requester.Username, sar.Spec.User)
				assert.Equal(t, "impersonate", sar.Spec.ResourceAttributes.Verb)
				sar.Status.Allowed = tc.allowedResource[sar.Spec.ResourceAttributes.Resource]
				return true, sar, nil
			})

			req := httptest.NewRequest(http.MethodGet, "/v1/harvester/harvesterhci.io.virtualmachineimages", nil)
			for k, values := range tc.headers {
				for _, v := range values {
					req.Header.Add(k, v)
				}
			}

			info, ok, err := newTokenImpersonationAuthenticator(client).Authenticate(req)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedOK, ok)
			assert.Equal(t, tc.expectedSARs, sarCount)
			if tc.expectedOK {
				assert.Equal(t, tc.expectedUser, info.GetName())
				assert.ElementsMatch(t, tc.expectedGroups, info.GetGroups())
			}
		})
	}
}
