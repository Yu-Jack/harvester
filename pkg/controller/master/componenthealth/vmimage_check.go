package componenthealth

import (
	"fmt"
	"strings"

	"github.com/rancher/wrangler/v3/pkg/condition"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
)

const (
	vmImageControllerName      = "componentHealthVMImageController"
	vmImageComponentHealthName = "harvester-controller-vmimage"
	vmImageKind                = "VirtualMachineImage"

	checkKeyImageRetryLimitExceeded   = "ImageRetryLimitExceeded"
	checkKeyImageUploadFailed         = "ImageUploadFailed"
	checkKeyImageImportFailed         = "ImageImportFailed"
	checkKeyImageInitializationFailed = "ImageInitializationFailed"
	checkKeyImageRetryingAfterFailure = "ImageRetryingAfterFailure"
	checkKeyImageBackingImageMissing  = "ImageBackingImageMissing"
)

func (h *Handler) OnVMImageChanged(_ string, image *harvesterv1.VirtualMachineImage) (*harvesterv1.VirtualMachineImage, error) {
	h.enqueueHealth(vmImageComponentHealthName)
	return image, nil
}

func (h *Handler) reconcileVMImages() error {
	images, err := h.vmImageCache.List(corev1.NamespaceAll, labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list VirtualMachineImages: %w", err)
	}

	checks := map[string]harvesterv1.CheckResult{}
	messages := map[string]map[string]struct{}{}
	for _, image := range images {
		if image.DeletionTimestamp != nil {
			continue
		}
		key, severity, cond := vmImageFailure(image)
		if key != "" {
			addVMImageCheck(checks, messages, image, key, severity, cond)
		}
		if harvesterv1.BackingImageMissing.IsTrue(image) {
			addVMImageCheck(checks, messages, image, checkKeyImageBackingImageMissing, harvesterv1.SeverityWarning, harvesterv1.BackingImageMissing)
		}
	}
	for key, check := range checks {
		check.Message = fmt.Sprintf("%d VirtualMachineImage(s): %s", check.AffectedCount, strings.Join(getSortedMessages(messages[key]), "; "))
		checks[key] = check
	}
	return h.updateComponentHealthChecks(vmImageComponentHealthName, checks, []string{
		checkKeyImageRetryLimitExceeded, checkKeyImageUploadFailed, checkKeyImageImportFailed,
		checkKeyImageInitializationFailed, checkKeyImageRetryingAfterFailure, checkKeyImageBackingImageMissing,
	})
}

func vmImageFailure(image *harvesterv1.VirtualMachineImage) (string, harvesterv1.Severity, condition.Cond) {
	if harvesterv1.ImageImported.IsTrue(image) {
		return "", "", ""
	}
	switch {
	case harvesterv1.ImageRetryLimitExceeded.IsTrue(image):
		return checkKeyImageRetryLimitExceeded, harvesterv1.SeverityError, harvesterv1.ImageRetryLimitExceeded
	case harvesterv1.ImageImported.IsFalse(image):
		if harvesterv1.ImageImported.GetReason(image) == "UploadFailed" {
			return checkKeyImageUploadFailed, harvesterv1.SeverityError, harvesterv1.ImageImported
		}
		return checkKeyImageImportFailed, harvesterv1.SeverityError, harvesterv1.ImageImported
	case harvesterv1.ImageInitialized.IsFalse(image):
		return checkKeyImageInitializationFailed, harvesterv1.SeverityError, harvesterv1.ImageInitialized
	case harvesterv1.ImageRetryLimitExceeded.IsFalse(image) && harvesterv1.ImageRetryLimitExceeded.GetMessage(image) != "":
		return checkKeyImageRetryingAfterFailure, harvesterv1.SeverityWarning, harvesterv1.ImageRetryLimitExceeded
	default:
		return "", "", ""
	}
}

func addVMImageCheck(checks map[string]harvesterv1.CheckResult, messages map[string]map[string]struct{}, image *harvesterv1.VirtualMachineImage, key string, severity harvesterv1.Severity, cond condition.Cond) {
	check, ok := checks[key]
	if !ok {
		check = harvesterv1.CheckResult{
			Severity: severity,
			AffectedResources: &harvesterv1.AffectedResources{
				APIVersion: harvesterv1.SchemeGroupVersion.String(),
				Kind:       vmImageKind,
				Names:      map[string]harvesterv1.AffectedResourceDetail{},
			},
		}
		messages[key] = map[string]struct{}{}
	}
	name := image.Namespace + "/" + image.Name
	check.AffectedResources.Names[name] = harvesterv1.AffectedResourceDetail{}
	check.AffectedCount = len(check.AffectedResources.Names)
	message := fmt.Sprintf("%s: %s=%s, reason=%q, message=%q, failed=%d, retry=%d, lastFailedTime=%q",
		name, cond, cond.GetStatus(image), cond.GetReason(image), cond.GetMessage(image),
		image.Status.Failed, image.Spec.Retry, image.Status.LastFailedTime)
	if cond == harvesterv1.BackingImageMissing {
		message = "previous backing image lookup failed (condition is not automatically cleared): " + message
	}
	messages[key][message] = struct{}{}
	checks[key] = check
}
