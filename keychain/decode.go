package keychain

import (
	"encoding/binary"
	"errors"
)

var (
	errEmptyPayload       = errors.New("empty payload")
	errOutOfBounds        = errors.New("payload out of bounds")
	errNegativeLevel      = errors.New("negative level")
	errNegativeRound      = errors.New("negative round")
	errNegativeFitnessLen = errors.New("negative fitness length")
)

// decodeSignPayload parses Tenderbake payloads and extracts (kind, level, round).
// It returns the *exact* watermarked bytes to be signed (i.e., raw).
//
// Supported watermarks:
//
//	0x11 — block
//	0x12 — preattestation
//	0x13 — attestation
//
// Notes:
//   - We assume tz4 (BLS) keys on the gadget. For Tenderbake attestations with tz4,
//     the SLOT field is NOT included in the signed payload (matches Octez logic).
//   - For blocks, the round is taken from the FITNESS blob tail (same offsets as Octez).
//
// Signature:
//
//	kind, level, round, signBytes, err
func DecodeAndValidateSignPayload(raw []byte) (SIGN_KIND, uint64, uint32, []byte, error) {
	if len(raw) < 1 {
		return UNSPECIFIED, 0, 0, nil, errEmptyPayload
	}

	switch raw[0] {
	case 0x11: // Tenderbake block
		const (
			levelOff    = 1 + 4
			fitnessOff  = 1 + 4 + 4 + 1 + 32 + 8 + 1 + 32
			blockMinLen = fitnessOff + 4 // Minimum length to read the fitness length
		)

		// 1. Perform a single bounds check for the fixed-size header.
		if len(raw) < blockMinLen {
			return UNSPECIFIED, 0, 0, nil, errOutOfBounds
		}

		// 2. Use encoding/binary for optimized, idiomatic reads.
		// Extract and validate level.
		levelI32 := int32(binary.BigEndian.Uint32(raw[levelOff:]))
		if levelI32 < 0 {
			return UNSPECIFIED, 0, 0, nil, errNegativeLevel
		}

		// Extract and validate fitness length.
		fitnessLenI32 := int32(binary.BigEndian.Uint32(raw[fitnessOff:]))
		if fitnessLenI32 < 0 {
			return UNSPECIFIED, 0, 0, nil, errNegativeFitnessLen
		}

		// Calculate round offset and check final bounds.
		roundOff := fitnessOff + int(fitnessLenI32)
		if roundOff+4 > len(raw) {
			return UNSPECIFIED, 0, 0, nil, errOutOfBounds
		}

		// Extract and validate round.
		roundI32 := int32(binary.BigEndian.Uint32(raw[roundOff:]))
		if roundI32 < 0 {
			return UNSPECIFIED, 0, 0, nil, errNegativeRound
		}

		return BLOCK, uint64(levelI32), uint32(roundI32), raw, nil

	case 0x12, 0x13: // Tenderbake preattestation/attestation
		const (
			levelOff  = 1 + 4 + 32 + 1
			roundOff  = levelOff + 4
			attMinLen = roundOff + 4
		)

		if len(raw) < attMinLen {
			return UNSPECIFIED, 0, 0, nil, errOutOfBounds
		}

		levelI32 := int32(binary.BigEndian.Uint32(raw[levelOff:]))
		if levelI32 < 0 {
			return UNSPECIFIED, 0, 0, nil, errNegativeLevel
		}

		roundI32 := int32(binary.BigEndian.Uint32(raw[roundOff:]))
		if roundI32 < 0 {
			return UNSPECIFIED, 0, 0, nil, errNegativeRound
		}

		if raw[0] == 0x12 {
			return PREATTESTATION, uint64(levelI32), uint32(roundI32), raw, nil
		}
		return ATTESTATION, uint64(levelI32), uint32(roundI32), raw, nil

	default:
		// This is a cold path; fmt.Errorf is acceptable here for better debug info.
		return UNSPECIFIED, 0, 0, nil, ErrUnsupportedOperation
	}
}
