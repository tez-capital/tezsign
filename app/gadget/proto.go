package main

import (
	"github.com/tez-capital/tezsign/keychain"
	"github.com/tez-capital/tezsign/signer"
	"google.golang.org/protobuf/proto"
)

func marshalOK(ok bool) []byte {
	b, _ := proto.Marshal(&signer.Response{
		Payload: &signer.Response_Ok{
			Ok: &signer.Ok{
				Ok: ok,
			},
		},
	})

	return b
}

func marshalErr(code uint32, msg string) []byte {
	b, _ := proto.Marshal(&signer.Response{
		Payload: &signer.Response_Error{
			Error: &signer.Error{
				Code:    code,
				Message: msg,
			},
		},
	})

	return b
}

func wipeReq(r *signer.Request) {
	switch p := r.Payload.(type) {
	case *signer.Request_Unlock:
		if p.Unlock != nil && p.Unlock.Passphrase != nil {
			keychain.MemoryWipe(p.Unlock.Passphrase)
			p.Unlock.Passphrase = nil
		}
	case *signer.Request_NewKeys:
		if p.NewKeys != nil && p.NewKeys.Passphrase != nil {
			keychain.MemoryWipe(p.NewKeys.Passphrase)
			p.NewKeys.Passphrase = nil
		}
	case *signer.Request_DeleteKeys:
		if p.DeleteKeys != nil && p.DeleteKeys.Passphrase != nil {
			keychain.MemoryWipe(p.DeleteKeys.Passphrase)
			p.DeleteKeys.Passphrase = nil
		}
	}
}
