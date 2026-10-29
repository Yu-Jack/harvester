// Package restic plugs the restic CLI into the data mover backup engine.
package restic

import (
	"context"
	"fmt"
	"strings"

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
	resticutil "github.com/harvester/harvester/pkg/util/restic"
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

// Tool is the restic implementation of datamover.Tool.
type Tool struct{}

var _ datamover.Tool = Tool{}

func (Tool) Name() string { return "restic" }

func (Tool) BackupType() harvesterv1.BackupType { return harvesterv1.Restic }

func (Tool) JobLabelKey() string { return resticutil.LabelResticJob }

func (Tool) PasswordKey() string { return resticutil.PasswordKey }

func (Tool) SnapshotTags(namespace, vmBackupName, volumeBackupName string) []string {
	return []string{
		resticutil.NamespaceTag(namespace),
		resticutil.VMBackupTag(vmBackupName),
		resticutil.SnapshotTag(volumeBackupName),
	}
}

func (Tool) NewRuntime(
	ctx context.Context,
	clientset kubernetes.Interface,
	secretName string,
	target *settings.BackupTarget,
) (*datamover.Runtime, bool, error) {
	repository, err := resticutil.Repository(target)
	if err != nil {
		return nil, false, err
	}
	runtime, hasCapacity, err := resticutil.NewJobRuntime(ctx, clientset, secretName, repository, resticutil.JobLabels(nil))
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
	// Ensure the repo exists before backing up. `restic snapshots` is the
	// canonical "is this a repo?" probe; on a fresh S3 prefix it fails and we
	// initialize. If another volume job wins the init race, the follow-up
	// snapshots probe accepts that as success.
	// pipefail propagates a failing io-mode read; without it restic backup
	// would happily store an empty snapshot from empty stdin.
	return fmt.Sprintf(
		"set -eo pipefail; "+
			"(%s snapshots > /dev/null 2>&1 || { %s init || %s snapshots > /dev/null 2>&1; }) && "+
			"/usr/bin/harvester io-mode -device %s -mode=read | %s -q backup --stdin --stdin-filename %s --tag=%s",
		runtime.Command, runtime.Command, runtime.Command,
		devicePath,
		runtime.Command, volumeName, strings.Join(tags, ","),
	)
}

func (Tool) CheckScript(runtime *datamover.Runtime, tags []string) string {
	return resticutil.SnapshotCheckCommand(runtime.Command, tags)
}

func (Tool) ForgetScript(runtime *datamover.Runtime, tags []string) string {
	return resticutil.ForgetCommand(runtime.Command, tags)
}
