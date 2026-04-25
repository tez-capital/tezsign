package common

import (
	"context"
	"time"

	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/secure"
	"github.com/tez-capital/tezsign/signerpb"
	"google.golang.org/protobuf/proto"
)

const (
	unlockBaseTimeout   = 5 * time.Second
	unlockPerKeyTimeout = 1500 * time.Millisecond
	unlockMaxTimeout    = 30 * time.Second
)

func unlockTimeout(keyCount int) time.Duration {
	if keyCount <= 0 {
		return unlockBaseTimeout
	}

	timeout := unlockBaseTimeout + time.Duration(keyCount-1)*unlockPerKeyTimeout
	if timeout > unlockMaxTimeout {
		return unlockMaxTimeout
	}

	return timeout
}

func ReqUnlockKeys(b *broker.Broker, keys []string, pass []byte) ([]*signerpb.PerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer secure.MemoryWipe(p)

	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Unlock{
			Unlock: &signerpb.UnlockRequest{
				KeyIds:     keys,
				Passphrase: p,
			},
		},
	}, unlockTimeout(len(keys)))
	if err != nil {
		return nil, err
	}

	return resp.GetUnlock().GetResults(), nil
}

func ReqLockKeys(b *broker.Broker, keys []string) ([]*signerpb.PerKeyResult, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Lock{
			Lock: &signerpb.LockRequest{
				KeyIds: keys,
			},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}

	return resp.GetLock().GetResults(), nil
}

func ReqStatus(b *broker.Broker) (*signerpb.StatusResponse, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Status{
			Status: &signerpb.StatusRequest{},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}

	return resp.GetStatus(), nil
}

func ReqSign(b *broker.Broker, tz4 string, rawMsg []byte) ([]byte, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Sign{
			Sign: &signerpb.SignRequest{
				Tz4:     tz4,
				Message: rawMsg,
			},
		},
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}

	s := resp.GetSign()

	return s.GetSignature(), nil
}

func ReqNewKeys(b *broker.Broker, keyIDs []string, pass []byte) ([]*signerpb.NewKeyPerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer secure.MemoryWipe(p)

	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_NewKeys{
			NewKeys: &signerpb.NewKeysRequest{
				KeyIds:     keyIDs,
				Passphrase: p,
			},
		},
	}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetNewKey().GetResults(), nil
}

func ReqDeleteKeys(b *broker.Broker, keyIDs []string, pass []byte) ([]*signerpb.PerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer secure.MemoryWipe(p)

	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_DeleteKeys{
			DeleteKeys: &signerpb.DeleteKeysRequest{
				KeyIds:     keyIDs,
				Passphrase: p,
			},
		},
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetDeleteKeys().GetResults(), nil
}

func ReqLogs(b *broker.Broker, limit int) ([]string, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Logs{
			Logs: &signerpb.LogsRequest{Limit: uint32(limit)},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetLogs().GetLines(), nil
}

func ReqInitMaster(b *broker.Broker, deterministic bool, pass []byte) (bool, error) {
	p := append([]byte(nil), pass...)
	defer secure.MemoryWipe(p)

	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_InitMaster{
			InitMaster: &signerpb.InitMasterRequest{
				Deterministic: deterministic,
				Passphrase:    p,
			},
		},
	}, 5*time.Second)
	if err != nil {
		return false, err
	}
	return resp.GetOk().GetOk(), nil
}

func ReqInitInfo(b *broker.Broker) (*signerpb.InitInfoResponse, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_InitInfo{InitInfo: &signerpb.InitInfoRequest{}},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetInitInfo(), nil
}

func ReqSetLevel(b *broker.Broker, keyID string, level uint64) (bool, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_SetLevel{
			SetLevel: &signerpb.SetLevelRequest{
				KeyId: keyID,
				Level: level,
			},
		},
	}, 3*time.Second)
	if err != nil {
		return false, err
	}
	return resp.GetOk().GetOk(), nil
}

func ReqVersion(b *broker.Broker) (*signerpb.VersionResponse, error) {
	resp, err := doReq(b, &signerpb.Request{
		Payload: &signerpb.Request_Version{
			Version: &signerpb.VersionRequest{},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetVersion(), nil
}

func doReq(b *broker.Broker, req *signerpb.Request, timeout time.Duration) (*signerpb.Response, error) {
	pb, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, _, err := b.Request(ctx, pb)
	if err != nil {
		return nil, err
	}
	var resp signerpb.Response
	if err := proto.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}

	if err := resp.GetError(); err != nil {
		return nil, &RemoteError{Code: err.Code, Msg: err.Message}
	}

	return &resp, nil
}

type RemoteError struct {
	Code uint32
	Msg  string
}

func (e *RemoteError) Error() string { return e.Msg }
