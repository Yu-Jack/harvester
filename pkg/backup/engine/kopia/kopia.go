// Package kopia plugs the kopia CLI into the data mover backup engine.
package kopia

import (
	"context"
	"fmt"

	ctlbatchv1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/batch/v1"
	ctlcorev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	ctlstoragev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/storage/v1"
	"k8s.io/client-go/kubernetes"

	harvesterv1 "github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/backup/common"
	"github.com/harvester/harvester/pkg/backup/engine"
	"github.com/harvester/harvester/pkg/backup/engine/datamover"
	ctlsnapshotv1 "github.com/harvester/harvester/pkg/generated/controllers/snapshot.storage.k8s.io/v1"
	"github.com/harvester/harvester/pkg/settings"
	kopiautil "github.com/harvester/harvester/pkg/util/kopia"
)

func GetBackupEngine(
	vmbo common.VMBackupOperator,
	vsCache ctlsnapshotv1.VolumeSnapshotCache,
	vsClient ctlsnapshotv1.VolumeSnapshotClient,
	pvcCache ctlcorev1.PersistentVolumeClaimCache,
	pvcClient ctlcorev1.PersistentVolumeClaimClient,
	secretCache ctlcorev1.SecretCache,
	secretClient ctlcorev1.SecretClient,
	scCache ctlstoragev1.StorageClassCache,
	jobController ctlbatchv1.JobController,
	clientset kubernetes.Interface,
) engine.BackupEngine {
	return datamover.NewEngine(Tool{}, vmbo, vsCache, vsClient, pvcCache, pvcClient,
		secretCache, secretClient, scCache, jobController, clientset)
}

// Tool is the kopia implementation of datamover.Tool.
type Tool struct{}

var _ datamover.Tool = Tool{}

func (Tool) Name() string { return "kopia" }

func (Tool) BackupType() harvesterv1.BackupType { return harvesterv1.Kopia }

func (Tool) JobLabelKey() string { return kopiautil.LabelKopiaJob }

func (Tool) PasswordKey() string { return kopiautil.PasswordKey }

func (Tool) SnapshotTags(namespace, vmBackupName, volumeBackupName string) []string {
	return []string{
		kopiautil.NamespaceTag(namespace),
		kopiautil.VMBackupTag(vmBackupName),
		kopiautil.SnapshotTag(volumeBackupName),
	}
}

func (Tool) NewRuntime(
	ctx context.Context,
	clientset kubernetes.Interface,
	secretName string,
	target *settings.BackupTarget,
) (*datamover.Runtime, bool, error) {
	repository, err := kopiautil.S3Repository(target)
	if err != nil {
		return nil, false, err
	}
	runtime, hasCapacity, err := kopiautil.NewJobRuntime(ctx, clientset, secretName, repository, kopiautil.JobLabels(nil))
	if err != nil || !hasCapacity {
		return nil, false, err
	}
	return &datamover.Runtime{
		Image:        runtime.Image,
		Labels:       runtime.Labels,
		Env:          runtime.Env,
		Resources:    runtime.Resources,
		VolumeMounts: runtime.VolumeMounts,
		Volumes:      runtime.Volumes,
		Command:      runtime.Command,
	}, true, nil
}

func (Tool) BackupScript(runtime *datamover.Runtime, devicePath, volumeName string, tags []string) string {
	return fmt.Sprintf(
		"set -eo pipefail\n%s\n/usr/bin/harvester io-mode -device %s -mode=read | %s snapshot create /%s --stdin-file %s %s",
		kopiautil.ConnectOrCreateCommand(),
		devicePath,
		runtime.Command,
		volumeName,
		volumeName,
		kopiautil.SnapshotTagsFlags(tags),
	)
}

func (Tool) CheckScript(_ *datamover.Runtime, tags []string) string {
	return kopiautil.SnapshotCheckCommand(kopiautil.ConnectCommand(), tags)
}

func (Tool) ForgetScript(_ *datamover.Runtime, tags []string) string {
	return kopiautil.SnapshotDeleteCommand(kopiautil.ConnectCommand(), tags)
}
