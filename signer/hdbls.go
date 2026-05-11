package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"math/big"

	"github.com/tez-capital/tezsign/secure"

	blst "github.com/supranational/blst/bindings/go"
)

var (
	// Field order r of BLS12-381 (scalar field Fr).
	bls12381_r = func() *big.Int {
		r, _ := new(big.Int).SetString("73EDA753299D7D483339D80809A1D80553BDA402FFFE5BFEFFFFFFFF00000001", 16)
		return r
	}()

	saltLabel = []byte("TEZSIGN-HD-V1|")
)

var (
	errMissingZeroFieldOrderR  = errors.New("invalid params: missing/zero field order r")
	errIkmInvalid              = errors.New("ikm must be >= 32 bytes")
	errLoadScalarFailed        = errors.New("failed to load scalar")
	errNilParent               = errors.New("parent is nil")
	errParentScalarSizeInvalid = errors.New("unexpected parent scalar size")
	errNilMaster               = errors.New("master is nil")
)

// ----- Parameters & helpers -----

// hdParams defines the scalar field order r and the HKDF salt used by HKDF_mod_r.
type hdParams struct {
	// r is the BLS12-381 scalar field order (Fr).
	// For BLS12-381 use: 0x73EDA753...00000001 (see bls12381_r).
	R    *big.Int
	Salt []byte // effective salt for HKDF-Extract
}

// TezSignHDParams builds HD params by mixing the fixed label with your master salt.
// salt := SHA256("TEZSIGN-HD-V1|" || masterSalt)
func TezSignHDParams(masterSalt []byte) hdParams {
	h := sha256.New()
	h.Write([]byte(saltLabel))
	h.Write(masterSalt)
	return hdParams{
		R:    bls12381_r,
		Salt: h.Sum(nil),
	}
}

// hkdfExtract returns HKDF-Extract(salt, ikm) with SHA-256.
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil) // 32 bytes
}

// hkdfExpand returns HKDF-Expand(prk, info, L) with SHA-256.
func hkdfExpand(prk, info []byte, L int) []byte {
	var (
		t   []byte
		out []byte
	)
	var mac hash.Hash
	var ctr byte = 1
	for len(out) < L {
		mac = hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{ctr})
		t = mac.Sum(nil)
		out = append(out, t...)
		ctr++
	}
	return out[:L]
}

func beToLE32(be []byte) []byte {
	le := make([]byte, 32)
	for i := 0; i < 32; i++ {
		le[i] = be[31-i]
	}
	return le
}

// ----- EIP-2333 core (HKDF_mod_r) -----

// hkdfModR implements EIP-2333 HKDF_mod_r (SHA-256) with pluggable salt and field order.
// Returns a *blst.SecretKey deterministically derived from ikm.
func hkdfModR(ikm []byte, params hdParams) (*blst.SecretKey, error) {
	if params.R == nil || params.R.Sign() <= 0 {
		return nil, errMissingZeroFieldOrderR
	}
	if len(ikm) < 32 {
		return nil, errIkmInvalid
	}
	// Start from provided salt; on zero result, salt = H(salt) and retry (EIP-2333).
	salt := append([]byte{}, params.Salt...) // copy
	for {
		prk := hkdfExtract(salt, ikm)
		okm := hkdfExpand(prk, nil, 48) // 48-byte big-endian candidate
		// sk = OS2IP(okm) mod r
		k := new(big.Int).SetBytes(okm)
		k.Mod(k, params.R)
		if k.Sign() != 0 {
			// Convert to 32-byte big-endian, then to LE for blst.
			var be [32]byte
			k.FillBytes(be[:])
			le := beToLE32(be[:])
			var sk blst.SecretKey
			if sk.FromLEndian(le) == nil {
				return nil, errLoadScalarFailed
			}
			return &sk, nil
		}
		// If zero, update salt = H(salt) and try again.
		h := sha256.Sum256(salt)
		salt = h[:]
	}
}

// deriveMasterSK deterministically derives a master SK from a seed using EIP-2333.
func deriveMasterSK(seed []byte, params hdParams) (*blst.SecretKey, error) {
	return hkdfModR(seed, params)
}

// deriveChildSK derives a hardened child SK from a parent SK and an index (EIP-2333).
// IKM = parent_sk_be32 || I2OSP(index, 4).
func deriveChildSK(parent *blst.SecretKey, index uint32, params hdParams) (*blst.SecretKey, error) {
	if parent == nil {
		return nil, errNilParent
	}
	// Try to get parent scalar as big-endian 32 bytes.
	// The Go blst binding reliably exposes LE; fall back via LE->BE conversion when needed.
	le := parent.ToLEndian()
	defer secure.MemoryWipe(le)
	if len(le) != 32 {
		return nil, errParentScalarSizeInvalid
	}
	// LE->BE
	be := beToLE32(le)
	defer secure.MemoryWipe(be)

	ikm := make([]byte, 0, 36)
	ikm = append(ikm, be...)
	defer secure.MemoryWipe(ikm)
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], index)
	ikm = append(ikm, idx[:]...)

	return hkdfModR(ikm, params)
}

// derivePathSK applies DeriveChildSK over a sequence of indices.
func derivePathSK(master *blst.SecretKey, path []uint32, params hdParams) (*blst.SecretKey, error) {
	if master == nil {
		return nil, errNilMaster
	}
	sk := master
	var err error
	for _, i := range path {
		parent := sk
		sk, err = deriveChildSK(parent, i, params)
		if parent != master {
			parent.Zeroize()
		}
		if err != nil {
			return nil, err
		}
	}
	return sk, nil
}

// ----- Public API -----

// GenerateRandomKey -> (secretKey, pubkeyBytes[48], BLpubkey = BLpk...)
func GenerateHDKey(masterSalt []byte, seed []byte, index uint32) (*blst.SecretKey, []byte, string, error) {
	params := TezSignHDParams(masterSalt)
	masterSK, err := deriveMasterSK(seed, params)
	if err != nil {
		return nil, nil, "", err
	}
	defer masterSK.Zeroize()
	path := []uint32{12381, 1729, 0, 0, index}
	childSK, err := derivePathSK(masterSK, path, params)
	if err != nil {
		return nil, nil, "", err
	}
	pubkeyBytes, blPubkey := PublicKeyFromSecret(childSK)

	return childSK, pubkeyBytes, blPubkey, nil
}
