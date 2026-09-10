package remote_device_svc

import "strings"

// IsSelfDevice reports whether deviceID is this installation's own canonical
// device fingerprint (the agentre-device-fingerprint keychain account, R5 —
// shared with LAN pairing and account login). After R13 canonicalization a
// local backend's DeviceID is the desktop's own fingerprint, so every branch
// that once keyed off be.IsLocal()/be.IsRemote() must also treat a self
// fingerprint as local. Only sha256:-prefixed named fingerprints can match;
// anything else short-circuits without a keychain read. Safe to call before
// bootstrap (returns false).
func IsSelfDevice(deviceID string) bool {
	if !strings.HasPrefix(deviceID, "sha256:") {
		return false
	}
	if defaultSvc == nil {
		return false
	}
	fp, err := defaultSvc.DeviceFingerprint()
	if err != nil || fp == "" {
		return false
	}
	return deviceID == fp
}

// TargetsAnotherMachine reports whether deviceID names a machine that is not this
// one — the question almost every caller actually has when it reaches for
// AgentBackend.IsRemote(). IsRemote() only asks "is DeviceID non-empty", and since
// R13 canonicalization a local backend carries this desktop's own fingerprint, so
// IsRemote() alone is true for the local machine too. Every dispatch, discovery and
// view boundary must therefore ask this instead; asking IsRemote() and forgetting
// the self check is the single recurring defect this predicate exists to prevent.
//
// Empty (never claimed) and the self fingerprint both mean "here"; anything else —
// a paired agentred, another desktop, or a fingerprint this installation has never
// seen — means "somewhere else". Unpaired is deliberately still "somewhere else":
// whether we can reach that machine is a separate question from whether it is ours.
func TargetsAnotherMachine(deviceID string) bool {
	return deviceID != "" && !IsSelfDevice(deviceID)
}

// ExternalDeviceID is the outward-facing reading of a backend's DeviceID, for every
// boundary that hands the value to something outside this installation's dispatch
// layer — view DTOs and portable export bundles alike. The empty string means "this
// machine", and after R13 canonicalization so does this installation's own
// fingerprint, so both collapse to empty on the way out.
//
// Leaking a self fingerprint outward breaks whoever receives it, because nothing on
// the outside can resolve it: a view mapper looks it up in paired_agentreds (a
// machine never pairs with itself), finds nothing, and renders the local machine as
// a nameless offline box; an import on another desktop finds no matching device in
// the bundle and rejects the whole backend as a dangling reference. Dispatch applies
// the same collapse (chat_svc.execDeviceID); this is that rule's single home for
// everything else.
func ExternalDeviceID(deviceID string) string {
	if !TargetsAnotherMachine(deviceID) {
		return ""
	}
	return deviceID
}
