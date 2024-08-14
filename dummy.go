package main

import (
	context2 "context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/golang/glog"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

type DynamicResource struct {
	Name      string                `json:"name"`
	Resources []DynamicResourceItem `json:"resources"`
}

type DynamicResourceItem struct {
	DeviceID string `json:"deviceID"`
	Health   string `json:"health"`
}

type PluginDevice struct {
	Resource string
	Devices  map[string]*pluginapi.Device
}

// DummyDeviceManager manages our dummy devices
type DummyDeviceManager struct {
	devices map[string]*PluginDevice
	socket  string
	server  *grpc.Server
	health  chan *pluginapi.Device
}

func (ddm *DummyDeviceManager) GetPreferredAllocation(ctx context2.Context, request *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	response := &pluginapi.PreferredAllocationResponse{}
	for _, req := range request.ContainerRequests {
		response.ContainerResponses = append(response.ContainerResponses, &pluginapi.ContainerPreferredAllocationResponse{
			DeviceIDs: req.AvailableDeviceIDs,
		})
	}
	return response, nil
}

// Init function for our dummy devices
func (ddm *DummyDeviceManager) Init() error {
	glog.Info("Initializing dummy device plugin...")
	return nil
}

// discoverDummyResources populates device list
// TODO: We currently only do this once at init, need to change it to do monitoring
//
//	and health state update
func (ddm *DummyDeviceManager) discoverDummyResources() error {
	glog.Info("Discovering dummy devices")
	raw, err := os.ReadFile("./dummyResources.json")
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	var devs []DynamicResource
	err = json.Unmarshal(raw, &devs)
	if err != nil {
		fmt.Println(err)
		return err
	}
	ddm.devices = make(map[string]*PluginDevice)
	for _, dev := range devs {
		newRes := PluginDevice{
			Resource: dev.Name,
			Devices:  make(map[string]*pluginapi.Device),
		}
		for _, devv := range dev.Resources {
			glog.Infof("Add device %s", devv.DeviceID)
			newRes.Devices[devv.DeviceID] = &pluginapi.Device{ID: devv.DeviceID, Health: devv.Health}
		}
		ddm.devices[dev.Name] = &newRes
	}

	glog.Infof("Devices found: %v", ddm.devices)
	return nil
}

// Start starts the gRPC server of the device plugin
func (ddm *DummyDeviceManager) Start() error {
	err := ddm.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", ddm.socket)
	if err != nil {
		return err
	}

	ddm.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterDevicePluginServer(ddm.server, ddm)

	go ddm.server.Serve(sock)

	// Wait for server to start by launching a blocking connection
	conn, err := grpc.NewClient(ddm.socket, grpc.WithInsecure(),
		grpc.WithIdleTimeout(5*time.Second),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
	)

	if err != nil {
		return err
	}

	conn.Close()

	go ddm.healthcheck()

	return nil
}

// Stop stops the gRPC server
func (ddm *DummyDeviceManager) Stop() error {
	if ddm.server == nil {
		return nil
	}

	ddm.server.Stop()
	ddm.server = nil

	return ddm.cleanup()
}

// healthcheck monitors and updates device status
// TODO: Currently does nothing, need to implement
func (ddm *DummyDeviceManager) healthcheck() error {
	for {
		glog.Info(ddm.devices)
		time.Sleep(60 * time.Second)
	}
}

func (ddm *DummyDeviceManager) cleanup() error {
	if err := os.Remove(ddm.socket); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// Register with kubelet
func Register(ddm *DummyDeviceManager) error {
	conn, err := grpc.NewClient("passthrough:///"+pluginapi.KubeletSocket, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			glog.Infof("Dialing %s", addr)
			d := &net.Dialer{}
			return d.DialContext(ctx, "unix", addr)
		}))
	defer conn.Close()
	if err != nil {
		return fmt.Errorf("device-plugin: cannot connect to kubelet service: %v", err)
	}
	client := pluginapi.NewRegistrationClient(conn)
	for _, res := range ddm.devices {
		glog.Infof("register resource %s", res.Resource)
		reqt := &pluginapi.RegisterRequest{
			Version: pluginapi.Version,
			// Name of the unix socket the device plugin is listening on
			// PATH = path.Join(DevicePluginPath, endpoint)
			Endpoint: "dummy.sock",
			// Schedulable resource name.
			ResourceName: res.Resource,
		}

		_, err = client.Register(context.Background(), reqt)
		if err != nil {
			return fmt.Errorf("device-plugin: cannot register to kubelet service: %v", err)
		}
		glog.Infof("register %s success", res.Resource)
	}
	return nil
}

// ListAndWatch lists devices and update that list according to the health status
func (ddm *DummyDeviceManager) ListAndWatch(emtpy *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	glog.Info("device-plugin: ListAndWatch start\n")
	resp := new(pluginapi.ListAndWatchResponse)
	for _, dev := range ddm.devices {
		glog.Info("resource ", dev.Resource)
		for _, devv := range dev.Devices {
			resp.Devices = append(resp.Devices, devv)
		}
	}
	glog.Info("resp.Devices ", resp.Devices)
	if err := stream.Send(resp); err != nil {
		glog.Errorf("Failed to send response to kubelet: %v", err)
	}

	for {
		select {
		case d := <-ddm.health:
			d.Health = pluginapi.Healthy
			resp := new(pluginapi.ListAndWatchResponse)
			for _, dev := range ddm.devices {
				glog.Info("resource ", dev.Resource)
				for _, devv := range dev.Devices {
					resp.Devices = append(resp.Devices, devv)
				}
			}
			glog.Info("resp.Devices ", resp.Devices)
			if err := stream.Send(resp); err != nil {
				glog.Errorf("Failed to send response to kubelet: %v", err)
			}
		}
	}
}

// Allocate devices
func (ddm *DummyDeviceManager) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	glog.Info("Allocate")
	responses := pluginapi.AllocateResponse{}
	for _, req := range reqs.ContainerRequests {
		for _, id := range req.DevicesIDs {
			for _, dev := range ddm.devices {
				if _, ok := dev.Devices[id]; !ok {
					glog.Errorf("Can't allocate interface %s", id)
					return nil, fmt.Errorf("invalid allocation request: unknown device: %s", id)
				}
			}
		}
		glog.Info("Allocated interfaces ", req.DevicesIDs)
		response := pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{"DUMMY_DEVICES": strings.Join(req.DevicesIDs, ",")},
		}
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}
	return &responses, nil
}

// GetDevicePluginOptions returns options to be communicated with Device Manager
func (ddm *DummyDeviceManager) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// PreStartContainer is called, if indicated by Device Plugin during registeration phase,
// before each container start. Device plugin can run device specific operations
// such as reseting the device before making devices available to the container
func (ddm *DummyDeviceManager) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func main() {
	flag.Parse()
	flag.Lookup("logtostderr").Value.Set("true")

	// Create new dummy device manager
	ddm := &DummyDeviceManager{
		devices: make(map[string]*PluginDevice),
		socket:  pluginapi.DevicePluginPath + "dummy.sock",
		health:  make(chan *pluginapi.Device),
	}

	// Populate device list
	err := ddm.discoverDummyResources()
	if err != nil {
		glog.Fatal(err)
	}

	// Respond to syscalls for termination
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start grpc server
	err = ddm.Start()
	if err != nil {
		glog.Fatalf("Could not start device plugin: %v", err)
	}
	glog.Infof("Starting to serve on %s", ddm.socket)

	// Registers with Kubelet.
	err = Register(ddm)
	if err != nil {
		glog.Fatal(err)
	}
	fmt.Printf("device-plugin registered\n")

	select {
	case s := <-sigs:
		glog.Infof("Received signal \"%v\", shutting down.", s)
		ddm.Stop()
		return
	}
}
