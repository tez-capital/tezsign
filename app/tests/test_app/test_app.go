package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"

	"github.com/mr-tron/base58"

	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/logging"
)

func main() {
	serial := flag.String("serial", "", "USB serial to select (optional)")
	list := flag.Bool("list", false, "List matching devices and exit")

	det := flag.Bool("deterministic", false, "enable deterministic HD seed (absent => random)")

	flag.Parse()

	logCfg := logging.NewConfigFromEnv()
	if logCfg.File == "" {
		logCfg.File = logging.DefaultFileInExecDir("host.log")
	}

	if err := logging.EnsureDir(logCfg.File); err != nil {
		panic("Could not create dir for path of configuration file!")
	}

	l, _ := logging.New(logCfg)

	l.Info("logging to file", "path", logging.CurrentFile())

	if *list {
		if infos, err := common.ListFFSDevices(l); err != nil {
			l.Error("list devices", slog.Any("err", err))
		} else if len(infos) == 0 {
			l.Info("no devices found",
				slog.String("vid", fmt.Sprintf("%04x", common.VID)),
				slog.String("pid", fmt.Sprintf("%04x", common.PID)))
		} else {
			for i, inf := range infos {
				l.Info("device",
					slog.Int("idx", i),
					slog.String("serial", inf.Serial),
					slog.String("manufacturer", inf.Manufacturer),
					slog.String("product", inf.Product),
				)
			}
		}
		return
	}

	// connect to gadget (USB FFS + broker inside common)
	session, err := common.Connect(common.ConnectParams{Serial: *serial, Logger: l})
	defer session.Close()
	if err != nil {
		l.Error("connect", slog.Any("err", err))
		return
	}

	b := session.Broker
	l.Info("connected", slog.String("serial", session.Serial))

	// ---- MOCK FLOW ----
	masterPass := []byte("pass") // demo only. In real life, prompt the user.
	key := "key1"

	// 0) (Optional) init master once — if already initialized, treat as OK.
	if ok, err := common.ReqInitMaster(b, *det, masterPass); err != nil {
		l.Warn("init master", slog.Any("err", err))
	} else if ok {
		l.Info("master initialized", slog.Bool("deterministic", *det))
	}

	// 1) status (cold boot — probably empty or no last-levels)
	status, _ := common.ReqStatus(b)
	l.Info("status (boot)", slog.Any("status", status))

	// 2) create key (with passphrase) if it doesn’t exist yet
	if nk, err := common.ReqNewKeys(b, []string{key}, masterPass); err == nil {
		l.Info("new key", slog.String("id", nk[0].GetKeyId()), slog.String("tz4", nk[0].GetTz4()))
	} else {
		// In the future, if it already exists, continue; only error out on non-exists errors
		l.Warn("new key failed", slog.Any("err", err))
	}

	// 2.a) SIGN WHILE LOCKED (should fail)
	// if _, _, _, err := reqSign(b, key, buildTenderbakePayload(0x11, 1, 0, []byte("locked-should-fail"))); err == nil {
	// 	l.Error("expected sign to fail while locked, but it succeeded")
	// } else {
	// 	l.Info("sign while locked failed as expected", slog.Any("err", err))
	// }

	// 2.b) UNLOCK WITH WRONG PASSWORD (should fail)
	// if rs, err := reqUnlock(b, []string{key}, []byte("wrongpass")); err == nil && rs[0].Ok {
	// 	l.Error("expected unlock with wrong password to fail, but it succeeded", "result", rs[0])
	// } else {
	// 	l.Info("unlock with wrong password failed as expected", slog.Any("err", rs[0].Error))
	// }

	// 3) unlock key1
	rs, err := common.ReqUnlockKeys(b, []string{key}, masterPass)
	if err != nil {
		l.Error("unlock", slog.Any("err", err))
		return
	}
	l.Info("unlocked", slog.String("key", key), slog.Any("result", rs[0]))

	// 4) grab last ~20 gadget log lines
	// if lines, err := reqLogs(b, 20); err == nil {
	// 	for _, ln := range lines {
	// 		l.Info("gadget log", slog.String("line", ln))
	// 	}
	// }

	// 5) create another key with auto id
	// if nk, err := reqNewKey(b, "", masterPass); err == nil {
	// 	l.Info("new key", slog.String("id", nk.GetKeyId()), slog.String("tz4", nk.GetTz4()))
	// } else {
	// 	// In the future, if it already exists, continue; only error out on non-exists errors
	// 	l.Warn("new key failed", slog.Any("err", err))
	// }

	signVPayloads(b, l, key)

	// 6) run roundtrip benchmark
	// benchmarkRoundtrip(b, l, key)

	// 7) sign a few messages across kinds, with increasing levels
	// doSignDemo(b, l, key)

	// 8) show status again
	status2, _ := common.ReqStatus(b)
	l.Info("status (after signing)", slog.Any("status", status2))

	// 9) try a stale-level sign (should be denied)
	// if _, _, _, err := reqSign(b, key, buildTenderbakePayload(0x11, 1, 0, []byte("block@1 again"))); err != nil {
	// 	l.Warn("expected stale-level error", slog.Any("err", err))
	// }

	// 10) lock key
	rs, err = common.ReqLockKeys(b, []string{key})
	if err != nil {
		l.Error("lock", slog.Any("err", err))
	}
	l.Info("locked", slog.String("key", key), slog.Any("result", rs[0]))
}

func signVPayloads(b *broker.Broker, l *slog.Logger, key string) {
	var pfxBLSignature = []byte{40, 171, 64, 207}

	blockPayload := "117a06a77000a06dd417fc89ce97287862c59ff018f096be938c81454efc8bead42633ffff40429a17460000000068ea92180466ae1df25437b553f9d772aade2115aedbcd8720ce06a0975e13bc4ac1f008320000002100000001020000000400a06dd40000000000000004ffffffff00000004000000009a033180f02da06bd0a583fbfde72695562efefba5a9801a1ce2583496a04fb749f0d48f769c5a3453f9d14b5a61b8a9964709ce1c168ddbe61fc10c2bb3c136000000009aadd15cdae80000000a"
	preattestationPayload := "127a06a77040130177ce031f1a1c769c5437509bdc3bd5dd56e7ec5cf90e2a1c24eebcd02414011200a067be0000000001af791d701cd5526bad82ccb7f540c0591b64ebb48b4bf9e73d50585caf99c6"
	attestationPayload := "137a06a77007507e2c5d933e80b0e40637244461d0b383e6689a8cebc7b4b11eaed736b7bb1502a200a063ec00000000aa1524d58f2e298833cec19aaea276ebe43b4fe12a71a256bf663113c34f4509"

	seq := []string{blockPayload, preattestationPayload, attestationPayload}

	for _, payload := range seq {
		raw, err := hex.DecodeString(payload)
		if err != nil {
			fmt.Printf("%s: bad hex: %v\n", payload, err)
			continue
		}
		sig, err := common.ReqSign(b, key, raw)
		if err != nil {
			l.Error("sign failed", slog.Any("err", err))
			continue
		}
		l.Info("signed", slog.Any("sig", b58CheckEncode(pfxBLSignature, sig)))

	}
}

// Base58Check(prefix || payload || doubleSHA256(prefix||payload)[0:4])
func b58CheckEncode(prefix, payload []byte) string {
	n := len(prefix) + len(payload)
	buf := make([]byte, n+4)
	copy(buf, prefix)
	copy(buf[len(prefix):], payload)

	sum1 := sha256.Sum256(buf[:n])
	sum2 := sha256.Sum256(sum1[:])
	copy(buf[n:], sum2[:4])

	return base58.Encode(buf)
}
