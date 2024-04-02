package usb

import (
	"context"
	"fmt"
	"sync"

	"github.com/spf13/pflag"
	"kubevirt.io/client-go/kubecli"

	"github.com/harvester/harvester/pkg/config"
)

func Register(ctx context.Context, management *config.Management, _ config.Options) error {
	usbDevice := management.HarvesterFactory.Harvesterhci().V1beta1().USBDevice()
	usbDeviceClaim := management.HarvesterFactory.Harvesterhci().V1beta1().USBDeviceClaim()

	clientConfig := kubecli.DefaultClientConfig(&pflag.FlagSet{})
	virtClient, err := kubecli.GetKubevirtClientFromClientConfig(clientConfig)
	if err != nil {
		fmt.Println(err)
	}

	usbDeviceController := &USBDeviceHandler{
		usb:        usbDevice,
		virtClient: virtClient,
		lock:       &sync.Mutex{},
	}
	usbDeviceController.init()

	usbDeviceClaimController := &USBDeviceClaimHandler{
		usbDeviceCache: usbDevice.Cache(),
		usbDeviceClaim: usbDeviceClaim,
		virtClient:     virtClient,
		lock:           &sync.Mutex{},
		devicePlugin:   map[string]*USBDevicePlugin{},
	}

	usbDeviceClaim.OnChange(ctx, "usbDeviceClaim-device-claim", usbDeviceClaimController.OnUSBDeviceClaimChanged)
	usbDeviceClaim.OnRemove(ctx, "usbDeviceClaim-device-claim-remove", usbDeviceClaimController.OnRemove)

	return nil
}
