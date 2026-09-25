package app

import "testing"

func TestSessionIDFromUserInfo(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		want int64
	}{
		{"float64(JSON 往返)", map[string]interface{}{"sessionID": float64(42)}, 42},
		{"int64", map[string]interface{}{"sessionID": int64(7)}, 7},
		{"int", map[string]interface{}{"sessionID": int(5)}, 5},
		{"缺失", map[string]interface{}{"other": 1}, 0},
		{"nil map", nil, 0},
		{"非数值类型", map[string]interface{}{"sessionID": true}, 0},
	}
	for _, c := range cases {
		if got := sessionIDFromUserInfo(c.in); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

// 点击任何一条本应用发出的通知都切回窗口；只有带具体会话的才再跳到那个会话。外部 agrctl
// 审批弹窗的通知没有会话（sessionID 0），点击后也要切回窗口让弹窗露出来（spec「外部调用
// 的审批弹窗」）。
func TestNotificationClickAction(t *testing.T) {
	cases := []struct {
		name     string
		in       map[string]interface{}
		wantShow bool
		wantSID  int64
	}{
		{"会话通知", map[string]interface{}{"sessionID": float64(42)}, true, 42},
		{"无会话的应用通知（外部审批）", map[string]interface{}{"sessionID": float64(0)}, true, 0},
		{"不是本应用的载荷", map[string]interface{}{"other": 1}, false, 0},
		{"nil map", nil, false, 0},
	}
	for _, c := range cases {
		show, sid := notificationClickAction(c.in)
		if show != c.wantShow || sid != c.wantSID {
			t.Errorf("%s: got (%v, %d) want (%v, %d)", c.name, show, sid, c.wantShow, c.wantSID)
		}
	}
}
