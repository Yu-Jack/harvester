package constant

import (
	"context"

	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/harvester/harvester/pkg/config"
	"github.com/harvester/harvester/pkg/server"
	"github.com/harvester/harvester/tests/framework/cluster"
)

const (
	HarvesterStartTimeOut = 20
)

type CombinedConfig struct {
	RawKubeConfig clientcmdapi.Config
	Options       config.Options
}

var (
	TestSuiteStartErrChan chan error
	TestCtx               context.Context
	TestCtxCancel         context.CancelFunc
	Harvester             *server.HarvesterServer

	KubeConfig       *restclient.Config
	KubeClientConfig clientcmd.ClientConfig
	TestCluster      cluster.Cluster
	Options          config.Options
	CombinedCfg      CombinedConfig

	TestResourceLabels = map[string]string{
		"harvester.test.io": "harvester-test",
	}
	TestVMBackupLabels = map[string]string{
		"harvester.test.io/type": "vm-backup",
	}
	Scaled *config.Scaled
)
