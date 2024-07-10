package backuptarget

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/harvester/harvester/pkg/controller/master/backup"
	harvesterServer "github.com/harvester/harvester/pkg/server/http"
	"github.com/harvester/harvester/pkg/settings"
	"github.com/harvester/harvester/pkg/util"
	"github.com/longhorn/backupstore"
	"github.com/rancher/apiserver/pkg/apierror"
	"github.com/rancher/wrangler/pkg/schemas/validation"

	// Although we don't use following drivers directly, we need to import them to register drivers.
	// NFS Ref: https://github.com/longhorn/backupstore/blob/3912081eb7c5708f0027ebbb0da4934537eb9d72/nfs/nfs.go#L47-L51
	// S3 Ref: https://github.com/longhorn/backupstore/blob/3912081eb7c5708f0027ebbb0da4934537eb9d72/s3/s3.go#L33-L37
	_ "github.com/longhorn/backupstore/nfs" //nolint
	_ "github.com/longhorn/backupstore/s3"  //nolint
	ctlcorev1 "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"

	"github.com/harvester/harvester/pkg/config"
	"github.com/harvester/harvester/pkg/generated/controllers/harvesterhci.io/v1beta1"
)

type HealthyHandler struct {
	context      context.Context
	settingCache v1beta1.SettingCache
	secretCache  ctlcorev1.SecretCache
}

func NewHealthyHandler(scaled *config.Scaled) http.Handler {
	handler := &HealthyHandler{
		context:      scaled.Ctx,
		settingCache: scaled.HarvesterFactory.Harvesterhci().V1beta1().Setting().Cache(),
		secretCache:  scaled.CoreFactory.Core().V1().Secret().Cache(),
	}
	return harvesterServer.NewHandler(handler)
}

func (h *HealthyHandler) Do(_ http.ResponseWriter, _ *http.Request) (interface{}, error) {
	backupTargetSetting, err := h.settingCache.Get(settings.BackupTargetSettingName)
	if err != nil {
		return nil, apierror.NewAPIError(validation.ServerError, fmt.Sprintf("can't get %s setting, error: %v", settings.BackupTargetSettingName, err))
	}
	if backupTargetSetting.Value == "" {
		return nil, apierror.NewAPIError(harvesterServer.BadRequest, fmt.Sprintf("%s setting is not set", settings.BackupTargetSettingName))
	}

	target, err := settings.DecodeBackupTarget(backupTargetSetting.Value)
	if err != nil {
		return nil, apierror.NewAPIError(validation.ServerError, fmt.Sprintf("can't decode %s setting %s, error: %v", settings.BackupTargetSettingName, backupTargetSetting.Value, err))
	}
	if target.IsDefaultBackupTarget() {
		return nil, apierror.NewAPIError(harvesterServer.BadRequest, fmt.Sprintf("can't check the backup target healthy, %s setting is not set", settings.BackupTargetSettingName))
	}

	if target.Type == settings.S3BackupType {
		secret, err := h.secretCache.Get(util.LonghornSystemNamespaceName, util.BackupTargetSecretName)
		if err != nil {
			//util.ResponseError(rw, http.StatusInternalServerError, fmt.Errorf("can't get backup target secret: %s/%s, error: %w", util.LonghornSystemNamespaceName, util.BackupTargetSecretName, err))
			return nil, apierror.NewAPIError(validation.ServerError, fmt.Sprintf("can't get backup target secret: %s/%s, error: %v", util.LonghornSystemNamespaceName, util.BackupTargetSecretName, err))
		}
		os.Setenv(backup.AWSAccessKey, string(secret.Data[backup.AWSAccessKey]))
		os.Setenv(backup.AWSSecretKey, string(secret.Data[backup.AWSSecretKey]))
		os.Setenv(backup.AWSEndpoints, string(secret.Data[backup.AWSEndpoints]))
		os.Setenv(backup.AWSCERT, string(secret.Data[backup.AWSCERT]))
	}

	_, err = backupstore.GetBackupStoreDriver(backup.ConstructEndpoint(target))
	if err != nil {
		return nil, apierror.NewAPIError(validation.ServerError, fmt.Sprintf("can't connect to backup target %+v, error: %v", target, err))
	}

	return harvesterServer.EmptyResponseBody, nil
}
