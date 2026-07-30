package backingimage

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	harvesterClientset "github.com/harvester/harvester/pkg/generated/clientset/versioned/typed/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/image/backend"
	"github.com/harvester/harvester/pkg/image/common"
	"github.com/harvester/harvester/pkg/util"
	"github.com/harvester/harvester/pkg/webhook/types"
)

type Validator struct {
	vmiv       common.VMIValidator
	restConfig *rest.Config
}

func GetValidator(vmiv common.VMIValidator, restConfig *rest.Config) backend.Validator {
	return &Validator{vmiv: vmiv, restConfig: restConfig}
}

// checkSourceImageAccess verifies the requesting user can GET the source VMI via impersonation.
func (biv *Validator) checkSourceImageAccess(request *types.Request, vmi *harvesterv1.VirtualMachineImage) error {
	sp := vmi.Spec.SecurityParameters
	if sp == nil || sp.SourceImageName == "" {
		return nil
	}

	logrus.Infof("=== backingimage validator Create: %s", request)
	logrus.Infof("=== user info: %v", request.UserInfo)

	return util.WithUserImpersonation(biv.restConfig, request.UserInfo.Username, request.UserInfo.Groups, func(cfg *rest.Config) error {
		client, err := harvesterClientset.NewForConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to create impersonated client: %w", err)
		}
		if _, err := client.VirtualMachineImages(sp.SourceImageNamespace).Get(context.Background(), sp.SourceImageName, metav1.GetOptions{}); err != nil {
			logrus.Infof("user %s has no access to source image %s/%s: %v", request.UserInfo.Username, sp.SourceImageNamespace, sp.SourceImageName, err)
			return fmt.Errorf("user %q is not allowed to access source image %s/%s", request.UserInfo.Username, sp.SourceImageNamespace, sp.SourceImageName)
		}
		return nil
	})
}

func (biv *Validator) Create(request *types.Request, vmi *harvesterv1.VirtualMachineImage) error {
	if err := biv.checkSourceImageAccess(request, vmi); err != nil {
		return err
	}

	if err := biv.vmiv.CheckDisplayName(vmi); err != nil {
		return err
	}

	if err := biv.vmiv.SCConsistency(nil, vmi); err != nil {
		return err
	}

	if err := biv.vmiv.CheckURL(vmi); err != nil {
		return err
	}

	if err := biv.vmiv.CheckSecurityParameters(vmi); err != nil {
		return err
	}

	if err := biv.vmiv.CheckImagePVC(request, vmi); err != nil {
		return err
	}

	return nil
}

func (biv *Validator) Update(oldVMI, newVMI *harvesterv1.VirtualMachineImage) error {
	if err := biv.vmiv.SCParametersConsistency(oldVMI, newVMI); err != nil {
		return err
	}

	if err := biv.vmiv.SCConsistency(oldVMI, newVMI); err != nil {
		return err
	}

	if err := biv.vmiv.SourceTypeConsistency(oldVMI, newVMI); err != nil {
		return err
	}

	if biv.vmiv.IsExportVolume(newVMI) {
		if err := biv.vmiv.PVCConsistency(oldVMI, newVMI); err != nil {
			return err
		}
	}

	if err := biv.vmiv.URLConsistency(oldVMI, newVMI); err != nil {
		return err
	}

	if err := biv.vmiv.SecurityParameterConsistency(oldVMI, newVMI); err != nil {
		return err
	}

	if err := biv.vmiv.CheckUpdateDisplayName(oldVMI, newVMI); err != nil {
		return err
	}

	if err := biv.vmiv.CheckURL(newVMI); err != nil {
		return err
	}

	return nil
}

func (biv *Validator) Delete(vmi *harvesterv1.VirtualMachineImage) error {
	if biv.vmiv.GetStatusSC(vmi) == "" {
		return nil
	}

	if err := biv.vmiv.VMTemplateVersionOccupation(vmi); err != nil {
		return err
	}

	if err := biv.vmiv.PVCOccupation(vmi); err != nil {
		return err
	}

	if err := biv.vmiv.VMBackupOccupation(vmi); err != nil {
		return err
	}

	return nil
}
