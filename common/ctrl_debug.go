package common

import (
	"fmt"
	"log/slog"

	"github.com/google/gousb"
	"github.com/tez-capital/tezsign/logging"
)

type ctrlSetup struct {
	BmReqType byte
	BReq      byte
	WValue    uint16
	WIndex    uint16
	WLength   uint16
}

func logCtrl(l *slog.Logger, tag string, s ctrlSetup, n int, data []byte, err error) {
	if err != nil {
		l.Error(tag,
			"bm", fmt.Sprintf("0x%02x", s.BmReqType),
			"bReq", fmt.Sprintf("0x%02x", s.BReq),
			"wValue", s.WValue, "wIndex", s.WIndex, "wLength", s.WLength,
			"err", err)
		return
	}
	hex := fmt.Sprintf("% x", data[:n])
	logging.All(l, tag,
		"bm", fmt.Sprintf("0x%02x", s.BmReqType),
		"bReq", fmt.Sprintf("0x%02x", s.BReq),
		"wValue", s.WValue, "wIndex", s.WIndex, "wLength", s.WLength,
		"n", n, "data", hex)
}

func ctrlIn(l *slog.Logger, d *gousb.Device, bm, bReq byte, wValue, wIndex, wLength uint16) (int, []byte, error) {
	buf := make([]byte, wLength)
	n, err := d.Control(bm, bReq, wValue, wIndex, buf)
	logCtrl(l, "CTRL-IN", ctrlSetup{bm, bReq, wValue, wIndex, wLength}, n, buf, err)
	return n, buf, err
}
