package common

import (
	"context"
	"time"

	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/keychain"
	"github.com/tez-capital/tezsign/signer"
	"google.golang.org/protobuf/proto"
)

func ReqUnlockKeys(b *broker.Broker, keys []string, pass []byte) ([]*signer.PerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer keychain.MemoryWipe(p)

	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_Unlock{
			Unlock: &signer.UnlockRequest{
				KeyIds:     keys,
				Passphrase: p,
			},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}

	return resp.GetUnlock().GetResults(), nil
}

func ReqLockKeys(b *broker.Broker, keys []string) ([]*signer.PerKeyResult, error) {
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_Lock{
			Lock: &signer.LockRequest{
				KeyIds: keys,
			},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}

	return resp.GetLock().GetResults(), nil
}

func ReqStatus(b *broker.Broker) (*signer.StatusResponse, error) {
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_Status{
			Status: &signer.StatusRequest{},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}

	return resp.GetStatus(), nil
}

func ReqSign(b *broker.Broker, tz4 string, rawMsg []byte) ([]byte, error) {
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_Sign{
			Sign: &signer.SignRequest{
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

func ReqNewKeys(b *broker.Broker, keyIDs []string, pass []byte) ([]*signer.NewKeyPerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer keychain.MemoryWipe(p)

	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_NewKeys{
			NewKeys: &signer.NewKeysRequest{
				KeyIds:     keyIDs,
				Passphrase: p,
			},
		},
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetNewKey().GetResults(), nil
}

func ReqDeleteKeys(b *broker.Broker, keyIDs []string, pass []byte) ([]*signer.PerKeyResult, error) {
	p := append([]byte(nil), pass...)
	defer keychain.MemoryWipe(p)

	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_DeleteKeys{
			DeleteKeys: &signer.DeleteKeysRequest{
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
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_Logs{
			Logs: &signer.LogsRequest{Limit: uint32(limit)},
		},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetLogs().GetLines(), nil
}

func ReqInitMaster(b *broker.Broker, deterministic bool, pass []byte) (bool, error) {
	p := append([]byte(nil), pass...)
	defer keychain.MemoryWipe(p)

	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_InitMaster{
			InitMaster: &signer.InitMasterRequest{
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

func ReqInitInfo(b *broker.Broker) (*signer.InitInfoResponse, error) {
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_InitInfo{InitInfo: &signer.InitInfoRequest{}},
	}, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return resp.GetInitInfo(), nil
}

func ReqSetLevel(b *broker.Broker, keyID string, level uint64) (bool, error) {
	resp, err := doReq(b, &signer.Request{
		Payload: &signer.Request_SetLevel{
			SetLevel: &signer.SetLevelRequest{
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

func doReq(b *broker.Broker, req *signer.Request, timeout time.Duration) (*signer.Response, error) {
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
	var resp signer.Response
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
