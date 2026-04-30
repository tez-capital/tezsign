package common

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/gousb"
	"github.com/tez-capital/tezsign/broker"
)

type DeviceInfo struct {
	Serial       string
	Manufacturer string
	Product      string
}

type ConnectParams struct {
	// If empty, first matching device is used (and a warning is logged if multiple).
	Serial string
	// Optional: if nil, a no-op logger is used.
	Logger *slog.Logger
	// Optional broker handler for incoming gadget->host requests (host is usually client-only).
	// If nil, a default handler that returns (nil, nil) is used.
	BrokerHandler broker.Handler
	// Optional write-idle keep-alive interval. Zero disables keep-alive.
	KeepAlive time.Duration
	Channel   Channel
}

type Channel int

const (
	ChanSign Channel = iota // IF0: sign
	ChanMgmt                // IF1: management
)

// Session owns the whole USB + broker stack and knows how to clean up.
type Session struct {
	Ctx *gousb.Context
	Dev *gousb.Device
	Cfg *gousb.Config

	Intf      *gousb.Interface
	InEp      *gousb.InEndpoint
	OutEp     *gousb.OutEndpoint
	Broker    *broker.Broker
	Channel   Channel
	KeepAlive time.Duration

	Serial string
	Log    *slog.Logger
}

// Close in reverse order of creation
func (s *Session) Close() {
	s.Broker.Stop() // this blocks until broker is fully stopped
	if s.Intf != nil {
		s.Intf.Close()
	}
	if s.Cfg != nil {
		_ = s.Cfg.Close()
	}
	if s.Dev != nil {
		_ = s.Dev.Close()
	}
	if s.Ctx != nil {
		_ = s.Ctx.Close()
	}
}

// ListFFSDevices lists all devices matching VID/PID with their serial/manufacturer/product.
func ListFFSDevices(l *slog.Logger) ([]DeviceInfo, error) {
	if l == nil {
		l = slog.New(slog.NewTextHandler(nil, nil))
	}

	ctx := gousb.NewContext()
	defer ctx.Close()

	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == gousb.ID(VID) && desc.Product == gousb.ID(PID)
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, d := range devs {
			_ = d.Close()
		}
	}()

	infos := make([]DeviceInfo, 0, len(devs))
	for _, d := range devs {
		sn, _ := d.SerialNumber()
		man, _ := d.Manufacturer()
		prod, _ := d.Product()
		infos = append(infos, DeviceInfo{
			Serial:       strings.TrimSpace(sn),
			Manufacturer: strings.TrimSpace(man),
			Product:      strings.TrimSpace(prod),
		})
	}
	return infos, nil
}

// ResetDevice opens the first matching VID/PID device (or a specific serial if provided),
// issues a USB port reset, and closes everything. No interface claim, no broker.
func ResetDevice(serial string, l *slog.Logger) error {
	if l == nil {
		l = slog.New(slog.NewTextHandler(nil, nil))
	}

	ctx := gousb.NewContext()
	defer ctx.Close()

	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == gousb.ID(VID) && desc.Product == gousb.ID(PID)
	})
	if err != nil {
		return fmt.Errorf("usb: open devices: %w", err)
	}
	defer func() {
		for _, d := range devs {
			_ = d.Close()
		}
	}()

	if len(devs) == 0 {
		return fmt.Errorf("%w: VID=%04x PID=%04x", ErrNoDevices, VID, PID)
	}

	// choose device
	var chosen *gousb.Device
	var chosenSerial string
	alts := make([]string, 0, len(devs))
	for _, d := range devs {
		sn, _ := d.SerialNumber()
		alts = append(alts, strings.TrimSpace(sn))
		if serial != "" && strings.TrimSpace(sn) == serial {
			chosen = d
			chosenSerial = strings.TrimSpace(sn)
		}
	}
	if serial == "" {
		if len(devs) > 1 {
			l.Warn("multiple devices found; resetting the first (override with --device)", slog.Any("serials", alts))
		}
		chosen = devs[0]
		chosenSerial, _ = chosen.SerialNumber()
		chosenSerial = strings.TrimSpace(chosenSerial)
	}

	if chosen == nil {
		return fmt.Errorf("%w: VID=%04x PID=%04x serial=%q (have: %v)", ErrDeviceNotFound, VID, PID, serial, alts)
	}

	l.Info("USB port reset", slog.String("serial", chosenSerial))
	if err := chosen.Reset(); err != nil {
		return fmt.Errorf("%w: %w", ErrUSBResetFailed, err)
	}
	return nil
}

// VendorReadyInInterface: IN | vendor | interface = 0x81; pass wIndex = interface number
func VendorReadyInInterface(d *gousb.Device, bRequest byte, iface uint16, l *slog.Logger) (bool, error) {
	n, buf, err := ctrlIn(l, d, bmReqTypeVendorIn, bRequest, 0, iface, 8)
	if err != nil {
		return false, fmt.Errorf("vendor ready (iface): %w", err)
	}
	if n < 7 || string(buf[:4]) != "TZSG" {
		return false, fmt.Errorf("bad reply (iface) n=%d", n)
	}
	return buf[6] == 1, nil
}

// Connect discovers vendor FFS interfaces, claims the requested channel, and returns ready brokers.
func Connect(p ConnectParams) (*Session, error) {
	l := p.Logger
	if l == nil {
		l = slog.New(slog.NewTextHandler(nil, nil))
	}

	// Ensure we always have a handler; host is client-only by default.
	if p.BrokerHandler == nil {
		p.BrokerHandler = func(ctx context.Context, payload []byte) ([]byte, error) {
			return nil, nil
		}
	}

	if p.Channel != ChanSign && p.Channel != ChanMgmt {
		return nil, ErrInvalidChannel
	}

	ctx := gousb.NewContext()

	// Open all matching VID/PID
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == gousb.ID(VID) && desc.Product == gousb.ID(PID)
	})
	if err != nil {
		ctx.Close()
		return nil, err
	}

	// Close all non-chosen devices; chosen one is transferred to session
	var chosen *gousb.Device
	var chosenSerial string
	defer func() {
		for _, d := range devs {
			if chosen == nil || d != chosen {
				_ = d.Close()
			}
		}
	}()

	if len(devs) == 0 {
		ctx.Close()
		return nil, fmt.Errorf("%w: VID=%04x PID=%04x", ErrNoDevices, VID, PID)
	}

	// Gather serials for diagnostics
	alts := make([]string, 0, len(devs))
	for _, d := range devs {
		sn, _ := d.SerialNumber()
		alts = append(alts, strings.TrimSpace(sn))
	}

	// If a specific serial is requested, find it
	if p.Serial != "" {
		for _, d := range devs {
			sn, _ := d.SerialNumber()
			if strings.TrimSpace(sn) == p.Serial {
				chosen = d
				chosenSerial = strings.TrimSpace(sn)
				break
			}
		}
		if chosen == nil {
			ctx.Close()
			return nil, fmt.Errorf("%w: VID=%04x PID=%04x serial=%q (have: %v)", ErrDeviceNotFound, VID, PID, p.Serial, alts)
		}
	} else {
		if len(devs) > 1 {
			l.Warn("multiple devices found; picking the first (override with --device)", slog.Any("serials", alts))
		}
		chosen = devs[0]
		chosenSerial, _ = chosen.SerialNumber()
		chosenSerial = strings.TrimSpace(chosenSerial)
	}

	// --- SAFE PROBE (do NOT set configuration yet) ---
	// Scan device descriptors for a vendor-specific (FFS) interface.
	// Some UDCs can wedge if the host selects a config before the FFS userspace
	// function has opened endpoints. Only touch the configuration once we see
	// the vendor-class interface advertised in the descriptors.
	type ifaceEndpoints struct {
		ifaceNum    int
		epIn, epOut gousb.EndpointAddress
	}

	var cfgNum = -1
	var ifaces []ifaceEndpoints

	for _, cfgDesc := range chosen.Desc.Configs {
		// collect all vendor-specific interfaces in this config
		var cand []ifaceEndpoints
		for _, iface := range cfgDesc.Interfaces {
			if len(iface.AltSettings) == 0 {
				continue
			}

			as := iface.AltSettings[0]
			if as.Class != gousb.ClassVendorSpec {
				continue
			}

			ie := ifaceEndpoints{ifaceNum: int(as.Number)}
			for _, ed := range as.Endpoints {
				if ed.TransferType != gousb.TransferTypeBulk {
					continue
				}
				if ed.Direction == gousb.EndpointDirectionIn {
					ie.epIn = ed.Address
				}
				if ed.Direction == gousb.EndpointDirectionOut {
					ie.epOut = ed.Address
				}
			}
			if ie.epIn != 0 && ie.epOut != 0 {
				cand = append(cand, ie)
			}

		}
		if len(cand) > 0 {
			cfgNum = int(cfgDesc.Number)
			ifaces = cand
			break
		}
	}
	if cfgNum == -1 || len(ifaces) == 0 {
		ctx.Close()
		_ = chosen.Close()
		return nil, ErrGadgetNotReady
	}

	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].ifaceNum < ifaces[j].ifaceNum })

	// Now it’s safe to select the discovered configuration and claim the chosen interface
	cfg, err := chosen.Config(cfgNum)
	if err != nil {
		ctx.Close()
		_ = chosen.Close()
		return nil, fmt.Errorf("config(%d): %w", cfgNum, err)
	}

	openPair := func(ie ifaceEndpoints) (*gousb.Interface, *gousb.InEndpoint, *gousb.OutEndpoint, error) {
		// Probe readiness before claiming the interface. Claiming too early can
		// perturb FunctionFS bring-up on some controllers when an auto-unlock
		// client connects the moment the device enumerates.
		if ok, err := VendorReadyInInterface(chosen, VendorReqReady, uint16(ie.ifaceNum), l); !ok {
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%w: iface %d: %w", ErrVendorProbeFailed, ie.ifaceNum, err)
			}
			return nil, nil, nil, fmt.Errorf("%w: iface %d", ErrInterfaceNotReady, ie.ifaceNum)
		}

		intf, err := cfg.Interface(ie.ifaceNum, 0)
		if err != nil {
			return nil, nil, nil, interfaceClaimError(ie.ifaceNum, err)
		}

		outEp, err := intf.OutEndpoint(int(ie.epOut & 0x0f))
		if err != nil {
			intf.Close()
			return nil, nil, nil, fmt.Errorf("open OUT iface %d: %w", ie.ifaceNum, err)
		}
		inEp, err := intf.InEndpoint(int(ie.epIn & 0x0f))
		if err != nil {
			intf.Close()
			return nil, nil, nil, fmt.Errorf("open IN iface %d: %w", ie.ifaceNum, err)
		}

		return intf, inEp, outEp, nil
	}

	// Choose exactly one interface: vendors[0] = IF0 (sign), vendors[1] = IF1 (management).
	pick := ifaces[0]

	if p.Channel == ChanMgmt {
		if len(ifaces) < 2 {
			_ = cfg.Close()
			ctx.Close()
			_ = chosen.Close()
			return nil, ErrNoManagementIface
		}
		pick = ifaces[1]
	}

	l.Debug("claiming interface",
		slog.Int("iface", pick.ifaceNum),
		slog.String("channel", map[Channel]string{ChanSign: "sign", ChanMgmt: "mgmt"}[p.Channel]),
	)

	openIntf, inEp, outEp, err := openPair(pick)

	if err != nil {
		_ = cfg.Close()
		ctx.Close()
		_ = chosen.Close()
		return nil, err
	}

	// Build broker
	brokerOpts := []broker.Option{
		broker.WithLogger(l.With("component", "broker", "chan", map[Channel]string{ChanSign: "sign", ChanMgmt: "mgmt"}[p.Channel])),
		broker.WithHandler(p.BrokerHandler),
	}
	if p.KeepAlive > 0 {
		brokerOpts = append(brokerOpts, broker.WithKeepAlive(p.KeepAlive))
	}
	br := broker.New(inEp, newLibusbWriter(outEp), brokerOpts...)

	l.Debug("using device", slog.String("serial", chosenSerial))

	return &Session{
		Ctx: ctx,
		Dev: chosen,
		Cfg: cfg,

		Intf:      openIntf,
		InEp:      inEp,
		OutEp:     outEp,
		Broker:    br,
		Channel:   p.Channel,
		KeepAlive: p.KeepAlive,

		Serial: chosenSerial,
		Log:    l,
	}, nil
}

func interfaceClaimError(ifaceNum int, err error) error {
	switch ifaceNum {
	case 0:
		return ErrSignInterfaceBusy
	case 1:
		return ErrMgmtInterfaceBusy
	default:
		return fmt.Errorf("%w: iface %d: %w", ErrInterfaceClaimFailed, ifaceNum, err)
	}
}
