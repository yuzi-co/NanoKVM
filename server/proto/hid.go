package proto

type GetHidModeRsp struct {
	Mode string `json:"mode"` // normal or hid-only
}

type GetKeyboardLedStatusRsp struct {
	NumLock    bool   `json:"numLock"`
	CapsLock   bool   `json:"capsLock"`
	ScrollLock bool   `json:"scrollLock"`
	Known      bool   `json:"known"`
	UpdatedAt  string `json:"updatedAt"`
}

// HidDeviceStatus reports whether the target is fetching reports from one HID
// endpoint.
//
// State carries a code rather than a sentence because the web UI translates it
// into about twenty languages. Detail holds the raw error text for an operator
// reading the response by hand, and is empty for the states that need none.
//
// ObservedMsAgo is the age of the observation. Nothing writes to an endpoint the
// operator has switched away from, so a stalled state goes stale rather than
// clearing, and a consumer must be able to tell the two apart.
type HidDeviceStatus struct {
	Name          string `json:"name"` // keyboard, mouse-relative, mouse-absolute
	Path          string `json:"path"`
	State         string `json:"state"` // unknown, accepting, stalled, error
	Detail        string `json:"detail,omitempty"`
	StateForMs    int64  `json:"stateForMs"`
	ObservedMsAgo int64  `json:"observedMsAgo"`
}

type GetHidStatusRsp struct {
	Devices []HidDeviceStatus `json:"devices"`
}

type SetHidModeReq struct {
	Mode string `validate:"required"` // normal or hid-only
}

type ShortcutKey struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type Shortcut struct {
	ID   string        `json:"id"`
	Keys []ShortcutKey `json:"keys"`
}

type GetShortcutsRsp struct {
	Shortcuts []Shortcut `json:"shortcuts"`
}

type AddShortcutReq struct {
	Keys []ShortcutKey `validate:"required"`
}

type DeleteShortcutReq struct {
	ID string `validate:"required"`
}

type SetLeaderKeyReq struct {
	Key string `validate:"omitempty"`
}

type GetLeaderKeyRsp struct {
	Key string `json:"key"`
}
