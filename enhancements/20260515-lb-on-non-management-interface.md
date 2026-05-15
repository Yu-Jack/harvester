# Support Load Balancers on Non-Management Interfaces for Guest Clusters

## Summary

When a guest cluster consists of multiple network interface, all load balancer IP addresses allocated for `LoadBalancer` type Services are still only assigned to the management interface, regardless of which network the IP pool is associated with. This enhancement makes the `harvester-cloud-provider` instruct kube-vip to bind the VIP to the correct network interface, matching the IP pool's associated network.

### Related Issues

https://github.com/harvester/harvester/issues/5486

## Motivation

kube-vip has no information about which interface to use and defaults to the management interface, defeating the purpose of network isolation when users configure dedicated IP pools for a workload network.

### Goals

- Load balancer VIPs are bound to the network interface that corresponds to the IP pool's associated VM Network.

### Non-goals

- Set up [`svc_election`](https://kube-vip.io/docs/usage/kubernetes-services/#load-balancing-load-balancers-when-using-arp-mode-yes-you-read-that-correctly-kube-vip-v050).
    - svc_election=false: The global leader node holds the VIP.
    - svc_election=true: Each service independently elects its own leader node.

## Introduction

Before this enhancement, we need to understand how the load balancer flow works across the two clusters involved.

The `harvester-cloud-provider` runs as a Deployment inside the guest cluster (`kube-system` namespace). It connects to two clusters simultaneously:

- **Guest cluster**: via in-cluster ServiceAccount credentials, to watch `LoadBalancer` type Services and update their status.
- **Harvester cluster**: via a `cloud-config` Secret mounted into the pod, to create and manage `LoadBalancer` CRs and query `VirtualMachineInstance` resources.

When a `LoadBalancer` type Service is created in the guest cluster, the flow is:

1. The Kubernetes cloud-controller-manager's built-in service controller detects the Service.
2. It calls `harvester-cloud-provider`'s `EnsureLoadBalancer()`.
3. The cloud provider creates a `LoadBalancer` CR on the Harvester cluster.
4. Harvester's load balancer controller allocates an IP from the matching IP pool.
5. The cloud provider writes the allocated IP to the Service annotation `kube-vip.io/loadbalancerIPs`.
6. kube-vip watches this annotation and binds the VIP to a network interface.

The problem is at step 6. kube-vip is not told which interface to use. Without a `vip_interface` configuration or a `kube-vip.io/serviceInterface` annotation, kube-vip selects the management interface by default.

The following illustrates the current broken state. A guest cluster node has two interfaces:

```
enp1s0 → net-mgmt (management, 192.168.100.0/24)
enp2s0 → net-101  (workload,    192.168.101.0/24)
```

After a `LoadBalancer` Service is created with an IP pool associated with `net-101`, the VIP `192.168.101.57` ends up on `enp1s0`:

```shell
$ ip addr show enp1s0
2: enp1s0: ...
    inet 192.168.100.103/24 ...
    inet 192.168.101.57/32 scope global enp1s0   ← VIP bound to wrong interface
```

## Proposal

kube-vip v0.8.0 introduced the `kube-vip.io/serviceInterface` per-Service annotation. When set to `auto`, kube-vip calls its internal `autoFindInterface()` function to locate the network interface whose subnet contains the allocated VIP, and binds the VIP there.

The `harvester-cloud-provider` already sets the `kube-vip.io/loadbalancerIPs` annotation to communicate the allocated IP to kube-vip. The fix is to additionally set `kube-vip.io/serviceInterface: auto` at the same time, allowing kube-vip to resolve the correct interface automatically based on the VIP's subnet.

### User Stories

#### Story 1

I run an RKE2 guest cluster on Harvester. Each node VM is attached to two networks: a management network (`net-mgmt`, 192.168.100.0/24) and a workload network (`net-101`, 192.168.101.0/24). I create an IP pool bound to `net-101` and deploy a web application exposed via a `LoadBalancer` Service. I expect all traffic to flow through `net-101`, keeping management traffic separate. With this enhancement, the VIP is automatically bound to the correct interface without any manual configuration.

#### Story 2

I operate two guest clusters on the same Harvester cluster, each with VMs attached to multiple networks. Both clusters share a management network (`192.168.122.0/24`) but have different workload network assignments:

- **Guest Cluster A**: management `192.168.122.0/24`, workload-1 `192.168.123.0/24`, workload-2 `192.168.124.0/24`
- **Guest Cluster B**: management `192.168.122.0/24`, workload-1 `192.168.124.0/24`, workload-2 `192.168.123.0/24`

When I create a `LoadBalancer` Service in Cluster A with an IP pool from `192.168.123.0/24`, the VIP is bound to the interface that carries `192.168.123.x` on Cluster A's nodes. When I create the same type of Service in Cluster B using a pool from `192.168.123.0/24`, the VIP is equally bound to the correct `192.168.123.x` interface on Cluster B's nodes — even though the two clusters assign their networks to different physical interfaces. Because the fix uses subnet-based auto-discovery at runtime, it works correctly for each cluster without hard-coding interface names.


### API Changes

A new annotation constant is added to `harvester-cloud-provider`:

```go
// pkg/cloud-controller-manager/annotation.go
KeyKubevipServiceInterface = "kube-vip.io/serviceInterface"
```

No new CRDs, no changes to existing Harvester APIs or the `LoadBalancer` CR schema.

## Design

### Implementation Overview

When `harvester-cloud-provider` detects a `LoadBalancer` type Service and writes the `kube-vip.io/loadbalancerIPs` annotation with the allocated VIP, it simultaneously writes `kube-vip.io/serviceInterface: auto`. This instructs kube-vip to auto-discover the correct interface at runtime by finding the interface whose configured subnet contains the VIP address.

Action Items:

- [ ] Add `KeyKubevipServiceInterface = "kube-vip.io/serviceInterface"` constant to `pkg/cloud-controller-manager/annotation.go`.
- [ ] Set `kube-vip.io/serviceInterface: auto` alongside `kube-vip.io/loadbalancerIPs` for both primary and secondary Services in `pkg/cloud-controller-manager/loadbalancer.go`.
- [ ] Update unit tests in `loadbalancer_test.go` to assert the new annotation is set.

### Test Plan

The detailed step-by-step procedure will be documented in the pull request. The high-level flow is:

1. In Harvester, create a VM Network for the workload subnet and a corresponding IP pool bound to that network.
2. Provision a guest cluster whose nodes are attached to both the management network and the workload network.
3. In the guest cluster UI, create an Nginx deployment and expose it as a `LoadBalancer` type Service, selecting the workload IP pool in the add-on configuration.
4. Wait for the Service to receive an external IP from the workload subnet.
5. SSH into the kube-vip leader node and verify the allocated IP is bound to the workload interface and not the management interface.
6. Enable `arp_ignore=1` on the node and confirm that a client within the workload subnet can successfully reach the Service, validating that the VIP is correctly placed on the workload interface.

### Upgrade Strategy

Existing `LoadBalancer` Services created before this change will not have the `kube-vip.io/serviceInterface` annotation set. After upgrading `harvester-cloud-provider`, the reconciler will automatically patch existing Services with the `kube-vip.io/serviceInterface: auto` annotation. However, adding the annotation alone is not sufficient. Users must restart the kube-vip DaemonSet once after the upgrade:

```shell
kubectl rollout restart daemonset kube-vip -n kube-system
```

New Services created after the upgrade will automatically receive the annotation, and kube-vip will bind the VIP to the correct interface when it processes the Service for the first time.

## Note
None.