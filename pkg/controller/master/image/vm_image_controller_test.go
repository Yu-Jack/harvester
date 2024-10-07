package image

import (
	"errors"
	"net/http"
	"testing"
	"time"

	longhorntypes "github.com/longhorn/longhorn-manager/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	faketesting "k8s.io/client-go/testing"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	fakeLH "github.com/harvester/harvester/pkg/generated/clientset/versioned/typed/longhorn.io/v1beta2/fake"
	"github.com/harvester/harvester/pkg/util"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func TestVMImageHandler_OnChanged(t *testing.T) {
	type input struct {
		image   *harvesterv1.VirtualMachineImage
		objects []runtime.Object
		reactor func(harvesterClientSet *fake.Clientset, k8sClientSet *k8sfake.Clientset)
	}
	var testCases = []struct {
		name     string
		given    input
		expected func(t *testing.T, handler *vmImageHandler, image *harvesterv1.VirtualMachineImage, err error)
	}{
		{
			name: "Test case 1: Create Encrypted Image",
			given: input{
				objects: []runtime.Object{
					&harvesterv1.VirtualMachineImage{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "source-image",
							Namespace: "default",
						},
						Spec: harvesterv1.VirtualMachineImageSpec{
							SourceType:  "download",
							URL:         "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/x86_64/alpine-standard-3.20.2-x86_64.iso",
							DisplayName: "source-image",
						},
					},
				},
				image: &harvesterv1.VirtualMachineImage{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "encrypted-test-image",
						Namespace: "default",
					},
					Spec: harvesterv1.VirtualMachineImageSpec{
						SourceType: "clone",
						SecurityParameters: &harvesterv1.VirtualMachineImageSecurityParameters{
							CryptoOperation:      "encrypt",
							SourceImageName:      "source-image",
							SourceImageNamespace: "default",
						},
						// After mutator, these parameters are from storage class of annotation
						// But, we didn't have mutator here, so we just put them here
						StorageClassParameters: map[string]string{
							util.LonghornOptionEncrypted:          "true",
							util.CSIProvisionerSecretNameKey:      "test-secret",
							util.CSIProvisionerSecretNamespaceKey: "default",
							util.CSINodeStageSecretNameKey:        "test-secret",
							util.CSINodeStageSecretNamespaceKey:   "default",
							util.CSINodePublishSecretNameKey:      "test-secret",
							util.CSINodePublishSecretNamespaceKey: "default",
							longhorntypes.OptionNumberOfReplicas:  "1",
						},
						DisplayName: "encrypted-test-image",
					},
				},
			},
			expected: func(t *testing.T, handler *vmImageHandler, _ *harvesterv1.VirtualMachineImage, err error) {
				bis, _ := handler.backingImageCache.List("longhorn-system", labels.Everything())
				assert.Equal(t, 1, len(bis))
				assert.Equal(t, "default/encrypted-test-image", bis[0].Annotations[util.AnnotationImageID])
				assert.Equal(t, "test-secret", bis[0].Spec.SourceParameters["secret"])
				assert.Equal(t, "default", bis[0].Spec.SourceParameters["secret-namespace"])
				assert.Nil(t, err)
			},
		},
		{
			name: "Test case 2: Failed to create backing image",
			given: input{
				objects: []runtime.Object{
					&harvesterv1.VirtualMachineImage{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "source-image",
							Namespace: "default",
						},
						Spec: harvesterv1.VirtualMachineImageSpec{
							SourceType:  "download",
							URL:         "https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/x86_64/alpine-standard-3.20.2-x86_64.iso",
							DisplayName: "source-image",
						},
					},
				},
				image: &harvesterv1.VirtualMachineImage{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "encrypted-test-image",
						Namespace: "default",
					},
					Spec: harvesterv1.VirtualMachineImageSpec{
						SourceType: "clone",
						SecurityParameters: &harvesterv1.VirtualMachineImageSecurityParameters{
							CryptoOperation:      "encrypt",
							SourceImageName:      "source-image",
							SourceImageNamespace: "default",
						},
						// After mutator, these parameters are from storage class of annotation
						// But, we didn't have mutator here, so we just put them here
						StorageClassParameters: map[string]string{
							util.LonghornOptionEncrypted:          "true",
							util.CSIProvisionerSecretNameKey:      "test-secret",
							util.CSIProvisionerSecretNamespaceKey: "default",
							util.CSINodeStageSecretNameKey:        "test-secret",
							util.CSINodeStageSecretNamespaceKey:   "default",
							util.CSINodePublishSecretNameKey:      "test-secret",
							util.CSINodePublishSecretNamespaceKey: "default",
							longhorntypes.OptionNumberOfReplicas:  "1",
						},
						DisplayName: "encrypted-test-image",
					},
				},
				reactor: func(harvesterClientSet *fake.Clientset, k8sClientSet *k8sfake.Clientset) {
					harvesterClientSet.LonghornV1beta2().(*fakeLH.FakeLonghornV1beta2).PrependReactor("create", "backingimages", func(action faketesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, errors.New("unexpected error")
					})
				},
			},
			expected: func(t *testing.T, handler *vmImageHandler, vmimage *harvesterv1.VirtualMachineImage, err error) {
				assert.Equal(t, true, harvesterv1.ImageRetryLimitExceeded.IsTrue(vmimage))
				assert.Equal(t, true, harvesterv1.ImageInitialized.IsFalse(vmimage))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harvesterClientSet := fake.NewSimpleClientset(append(tc.given.objects, tc.given.image)...)
			k8sClientSet := k8sfake.NewSimpleClientset()
			if tc.given.reactor != nil {
				tc.given.reactor(harvesterClientSet, k8sClientSet)
			}

			handler := &vmImageHandler{
				backingImages:     fakeclients.BackingImageClient(harvesterClientSet.LonghornV1beta2().BackingImages),
				backingImageCache: fakeclients.BackingImageCache(harvesterClientSet.LonghornV1beta2().BackingImages),
				storageClasses:    fakeclients.StorageClassClient(k8sClientSet.StorageV1().StorageClasses),
				storageClassCache: fakeclients.StorageClassCache(k8sClientSet.StorageV1().StorageClasses),
				images:            fakeclients.VirtualMachineImageClient(harvesterClientSet.HarvesterhciV1beta1().VirtualMachineImages),
				imageController:   fakeclients.VirtualMachineImageClient(harvesterClientSet.HarvesterhciV1beta1().VirtualMachineImages),
				httpClient: http.Client{
					Timeout: 15 * time.Second,
				},
				pvcCache: fakeclients.PersistentVolumeClaimCache(k8sClientSet.CoreV1().PersistentVolumeClaims),
			}

			image, err := handler.OnChanged("", tc.given.image)

			tc.expected(t, handler, image, err)
		})
	}
}
