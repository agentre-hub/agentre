package blocks

import "encoding/json"

// UnsupportedRequestNoticeKind is the noticeKind chat_svc/message_projection
// and agentre-ui both key off of to recognize this notice among the shared
// cago NoticeBlock carrier — the same structured-notice pattern provider
// fallback/switch notices use (chat_svc/view.ProviderNotice), just a
// different Kind sharing the same Text field.
const UnsupportedRequestNoticeKind = "hermes_unsupported_request"

// UnsupportedRequestNoticePayload is the small JSON encoded into cago
// blocks.NoticeBlock.Text for a backend reverse request Agentre could not
// answer (agentruntime.UnsupportedRequestNotice). Purpose mirrors
// agentruntime.UnsupportedRequestPurpose's closed vocabulary — never the
// protocol method name or raw params, which must never enter the transcript
// or a log line (spec 2026-09-17 "Unsupported Hermes requests"). agentre-ui
// resolves the sentence through i18n keyed on Purpose, never rendering a
// backend-supplied string directly.
type UnsupportedRequestNoticePayload struct {
	Kind    string `json:"kind"`
	Purpose string `json:"purpose"`
}

// EncodeUnsupportedRequestNotice encodes purpose into a NoticeBlock.Text
// payload.
func EncodeUnsupportedRequestNotice(purpose string) string {
	b, _ := json.Marshal(UnsupportedRequestNoticePayload{Kind: UnsupportedRequestNoticeKind, Purpose: purpose})
	return string(b)
}

// DecodeUnsupportedRequestNotice decodes a NoticeBlock.Text payload previously
// produced by EncodeUnsupportedRequestNotice. ok=false for any other notice
// shape (provider fallback/switch, unstructured legacy text, ...) so callers
// fall back to rendering Text as-is.
func DecodeUnsupportedRequestNotice(text string) (UnsupportedRequestNoticePayload, bool) {
	if text == "" {
		return UnsupportedRequestNoticePayload{}, false
	}
	var p UnsupportedRequestNoticePayload
	if err := json.Unmarshal([]byte(text), &p); err != nil || p.Kind != UnsupportedRequestNoticeKind {
		return UnsupportedRequestNoticePayload{}, false
	}
	return p, true
}
