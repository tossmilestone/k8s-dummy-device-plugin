# k8s-dummy-device-plugin

K8s Dummy Device Plugin *(for testing purpose only)*

This is a plugin that's used for testing and exploring [Kubernetes Device Plugins](https://kubernetes.io/docs/concepts/cluster-administration/device-plugins/).

In essence, it works as a kind of echo device. One specifies the (albeit pretend) devices in a JSON file, and the plugin operates on those, and allocates the devices to containers that request them -- it does this by setting those devices into environment variables in those containers.

### Update 14 August 2024
Support faking multiple devices in config.

Add devices in `dummyResources.json` as following:
```json
[
  {
    "name": "nvidia.com/gpu",
    "resources": [
      {
        "deviceID": "dev_1",
        "health": "Healthy"
      },
      {
        "deviceID": "dev_2",
        "health": "Healthy"
      },
      {
        "deviceID": "dev_3",
        "health": "Healthy"
      },
      {
        "deviceID": "dev_4",
        "health": "Healthy"
      }
    ]
  }
]
```
Every item in the list represents a device with resources.

The `name` is the key of the device, `resources` is the resource list of the device.

### Update 27 January 2021
creates `nvidia.com/gpu` device for testing. 
`examples/daemonset.yml` contains example deployment with config map
Specify number of GPUs in config map
Set label `fake-device-plugin: 'true'` on nodes to activate
## Building

This plugin is built by simply building the `dummy.go` file. Make sure your `$GOPATH` is set correctly and build with:

```
go build dummy.go
```

Dependencies are managed and versioned internally with [dep](https://github.com/golang/dep).

## Example Usage (when deployed as DaemonSet)

In the `./examples/` directory there is an example DaemonSet that will deploy the device plugin on each node in your cluster.

```
kubectl create -f ./examples/daemonset.yml
```

Then create the sample pod, available as `./sample_pod.yaml` in this repository.

```
$ kubectl create -f ./sample_pod.yaml
```

You may then see that the "devices" were created as environment variables.

```
$ kubectl exec -it dummy-pod -- /bin/sh -c "printenv" | grep DUMMY_DEVICES
DUMMY_DEVICES=dev_3,dev_4
```

## Configuration

Configuration of the "pretend" devices are in the `./dummyResources.json` file.

More configuration to come.