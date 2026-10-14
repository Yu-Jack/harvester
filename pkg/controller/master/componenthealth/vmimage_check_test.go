package componenthealth

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func TestReconcileVMImages(t *testing.T) {
	tests := []struct {
		name       string
		conditions []harvesterv1.Condition
		key        string
		severity   harvesterv1.Severity
		deleting   bool
	}{
		{name: "new image"},
		{name: "deleting image is excluded", deleting: true, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageRetryLimitExceeded, Status: corev1.ConditionTrue, Message: "download failed"},
		}},
		{name: "normal import", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionUnknown, Reason: "Importing", Message: "downloading"},
		}},
		{name: "metadata absent is not a failure", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionTrue},
		}},
		{name: "successful import ignores historical failures", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionTrue},
			{Type: harvesterv1.ImageRetryLimitExceeded, Status: corev1.ConditionFalse, Message: "old failure"},
		}},
		{name: "retry failure without reason", key: checkKeyImageRetryingAfterFailure, severity: harvesterv1.SeverityWarning, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageRetryLimitExceeded, Status: corev1.ConditionFalse, Message: "download failed"},
		}},
		{name: "empty retry message", conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageRetryLimitExceeded, Status: corev1.ConditionFalse},
		}},
		{name: "retry exhaustion takes priority", key: checkKeyImageRetryLimitExceeded, severity: harvesterv1.SeverityError, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageRetryLimitExceeded, Status: corev1.ConditionTrue, Message: "download failed"},
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionFalse, Message: "download failed"},
			{Type: harvesterv1.ImageInitialized, Status: corev1.ConditionFalse, Message: "initialization failed"},
		}},
		{name: "upload failed", key: checkKeyImageUploadFailed, severity: harvesterv1.SeverityError, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionFalse, Reason: "UploadFailed", Message: "upload failed"},
		}},
		{name: "import failed without reason", key: checkKeyImageImportFailed, severity: harvesterv1.SeverityError, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionFalse, Message: "import failed"},
		}},
		{name: "initialization failed", key: checkKeyImageInitializationFailed, severity: harvesterv1.SeverityError, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageInitialized, Status: corev1.ConditionFalse, Message: "initialization failed"},
		}},
		{name: "missing backing image after import", key: checkKeyImageBackingImageMissing, severity: harvesterv1.SeverityWarning, conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionTrue},
			{Type: harvesterv1.BackingImageMissing, Status: corev1.ConditionTrue, Message: "backing image not found"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			image := &harvesterv1.VirtualMachineImage{
				ObjectMeta: metav1.ObjectMeta{Name: "image", Namespace: "default"},
				Spec:       harvesterv1.VirtualMachineImageSpec{Retry: 3},
				Status:     harvesterv1.VirtualMachineImageStatus{Conditions: tc.conditions, Failed: 2, LastFailedTime: "2026-10-14T10:55:32Z"},
			}
			if tc.deleting {
				deletedAt := metav1.Now()
				image.DeletionTimestamp = &deletedAt
			}
			clientset := fake.NewSimpleClientset(image)
			handler := &Handler{
				componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				vmImageCache:     fakeclients.VirtualMachineImageCache(clientset.HarvesterhciV1beta1().VirtualMachineImages),
			}
			if !assert.NoError(t, handler.reconcileVMImages()) {
				return
			}
			health, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), vmImageComponentHealthName, metav1.GetOptions{})
			if !assert.NoError(t, err) {
				return
			}
			if tc.key == "" {
				assert.Empty(t, health.Status.Checks)
				return
			}
			assert.Len(t, health.Status.Checks, 1)
			check := health.Status.Checks[tc.key]
			assert.Equal(t, tc.severity, check.Severity)
			assert.Equal(t, 1, check.AffectedCount)
			assert.Equal(t, vmImageKind, check.AffectedResources.Kind)
			assert.Equal(t, harvesterv1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
			assert.Contains(t, check.AffectedResources.Names, "default/image")
			assert.Contains(t, check.Message, "failed=2, retry=3")
			assert.Contains(t, check.Message, image.Status.LastFailedTime)
			if tc.key == checkKeyImageBackingImageMissing {
				assert.Contains(t, check.Message, "not automatically cleared")
			}
			for _, cond := range tc.conditions {
				if cond.Message != "" && (tc.key != checkKeyImageRetryLimitExceeded || cond.Type == harvesterv1.ImageRetryLimitExceeded) {
					assert.Contains(t, check.Message, cond.Message)
				}
			}
		})
	}
}

func TestVMImageChecksClearAndPreserveNamespacedResources(t *testing.T) {
	ctx := context.Background()
	image := &harvesterv1.VirtualMachineImage{
		ObjectMeta: metav1.ObjectMeta{Name: "image", Namespace: "default"},
		Status: harvesterv1.VirtualMachineImageStatus{Conditions: []harvesterv1.Condition{
			{Type: harvesterv1.ImageImported, Status: corev1.ConditionFalse, Reason: "UploadFailed", Message: "upload failed"},
			{Type: harvesterv1.BackingImageMissing, Status: corev1.ConditionTrue, Message: "lookup failed"},
		}},
	}
	other := image.DeepCopy()
	other.Namespace = "other"
	clientset := fake.NewSimpleClientset(image, other)
	controller := &recordingComponentHealthController{}
	handler := &Handler{
		componentHealthController: controller,
		componentHealths:          fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		vmImageCache:              fakeclients.VirtualMachineImageCache(clientset.HarvesterhciV1beta1().VirtualMachineImages),
	}
	result, err := handler.OnVMImageChanged("default/image", image)
	assert.Same(t, image, result)
	assert.NoError(t, err)
	assert.Equal(t, []string{vmImageComponentHealthName}, controller.enqueued)
	if !assert.NoError(t, handler.reconcileVMImages()) {
		return
	}
	health, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(ctx, vmImageComponentHealthName, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	assert.Len(t, health.Status.Checks, 2)
	check := health.Status.Checks[checkKeyImageUploadFailed]
	assert.Equal(t, 2, check.AffectedCount)
	assert.Contains(t, check.AffectedResources.Names, "default/image")
	assert.Contains(t, check.AffectedResources.Names, "other/image")
	assert.Contains(t, check.Message, "default/image")
	assert.Contains(t, check.Message, "other/image")

	image.Status.Conditions = []harvesterv1.Condition{{Type: harvesterv1.ImageImported, Status: corev1.ConditionTrue}}
	if _, err := clientset.HarvesterhciV1beta1().VirtualMachineImages(image.Namespace).Update(ctx, image, metav1.UpdateOptions{}); !assert.NoError(t, err) {
		return
	}
	if !assert.NoError(t, handler.reconcileVMImages()) {
		return
	}
	health, err = clientset.HarvesterhciV1beta1().ComponentHealths().Get(ctx, vmImageComponentHealthName, metav1.GetOptions{})
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, 1, health.Status.Checks[checkKeyImageUploadFailed].AffectedCount)
	assert.NotContains(t, health.Status.Checks[checkKeyImageUploadFailed].AffectedResources.Names, "default/image")

	if !assert.NoError(t, clientset.HarvesterhciV1beta1().VirtualMachineImages(other.Namespace).Delete(ctx, other.Name, metav1.DeleteOptions{})) {
		return
	}
	result, err = handler.OnVMImageChanged("other/image", nil)
	assert.Nil(t, result)
	assert.NoError(t, err)
	_, err = handler.OnComponentHealthChanged(controller.enqueued[1], health)
	if !assert.NoError(t, err) {
		return
	}
	health, err = clientset.HarvesterhciV1beta1().ComponentHealths().Get(ctx, vmImageComponentHealthName, metav1.GetOptions{})
	if assert.NoError(t, err) {
		assert.Empty(t, health.Status.Checks)
	}
}

func TestReconcileVMImagesListError(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("list", "virtualmachineimages", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("cache failure")
	})
	handler := &Handler{vmImageCache: fakeclients.VirtualMachineImageCache(clientset.HarvesterhciV1beta1().VirtualMachineImages)}
	assert.EqualError(t, handler.reconcileVMImages(), "failed to list VirtualMachineImages: cache failure")
}
