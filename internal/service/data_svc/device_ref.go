package data_svc

import "github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"

type deviceRefResolver struct {
	bundleUUIDs map[string]struct{}
	localUUIDs  map[string]struct{}
}

func newDeviceRefResolver(localDevices []*paired_agentred_entity.PairedAgentred, bundleDevices []BundleRemoteDevice) *deviceRefResolver {
	r := &deviceRefResolver{
		bundleUUIDs: make(map[string]struct{}, len(bundleDevices)),
		localUUIDs:  make(map[string]struct{}, len(localDevices)),
	}
	for _, d := range bundleDevices {
		if d.InstanceUUID != "" {
			r.bundleUUIDs[d.InstanceUUID] = struct{}{}
		}
	}
	for _, d := range localDevices {
		if d == nil {
			continue
		}
		if d.InstanceUUID != "" {
			r.localUUIDs[d.InstanceUUID] = struct{}{}
		}
	}
	return r
}

func (r *deviceRefResolver) StableKey(ref string) (string, bool) {
	if r == nil || ref == "" {
		return "", false
	}
	if _, ok := r.bundleUUIDs[ref]; ok {
		return ref, true
	}
	if _, ok := r.localUUIDs[ref]; ok {
		return ref, true
	}
	return "", false
}
