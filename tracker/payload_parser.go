package tracker

import (
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/Ethernal-Tech/solana-infrastructure/sendtx"
	"github.com/Ethernal-Tech/solana-infrastructure/wallet"
	"github.com/gagliardetto/solana-go"
)

// BridgeInstructionDiscriminator is the Anchor discriminator for bridge_transaction.
// Source: IDL discriminator field [73, 26, 119, 117, 56, 168, 209, 98]
var BridgeInstructionDiscriminator = [8]byte{73, 26, 119, 117, 56, 168, 209, 98}

// BridgeInstructionArgs is the fully decoded bridge_transaction instruction,
// shaped to match the caller's domain types.
type BridgeInstructionArgs struct {
	Receivers []sendtx.BridgingTxReceiver `json:"receivers"`
	BatchID   uint64                      `json:"batch_id"`
}

// ParseBridgeInstructionData decodes the raw instruction data byte slice
// for a bridge_transaction instruction into BridgeInstructionArgs.
//
// Expected binary layout (Anchor/Borsh), matching the on-chain TransferItem struct:
//
//	[0..8]            discriminator   – must equal BridgeInstructionDiscriminator
//	[8..12]           transfers_len   – u32 LE, number of TransferItem entries
//	per TransferItem (41 bytes each):
//	  [+0..+32]       recipient       – raw 32-byte ed25519 public key
//	  [+32]           mint_index      – u8, zero-based index into the mints vec
//	  [+33..+41]      amount          – u64 LE, token units in mint's base unit
//	[after transfers] mints_len       – u32 LE, number of deduplicated mint pubkeys
//	per mint:
//	  [+0..+32]       mint pubkey     – raw 32-byte ed25519 public key
//	[after mints]     batch_id        – u64 LE
//
// The mints vec is deduplicated: multiple transfers may share the same mint via
// mint_index, so mints_len <= transfers_len (not necessarily equal).
func ParseBridgeInstructionData(data []byte) (*BridgeInstructionArgs, error) {
	const (
		discriminatorSize = 8
		u32Size           = 4
		u64Size           = 8
		u8Size            = 1
		pubkeySize        = 32
		// TransferItem = recipient(32) + mint_index(1) + amount(8) = 41 bytes
		transferItemSize = pubkeySize + u8Size + u64Size
	)

	if len(data) < discriminatorSize {
		return nil, fmt.Errorf("data too short: need at least %d bytes for discriminator, got %d",
			discriminatorSize, len(data))
	}

	// Validate discriminator.
	var disc [8]byte

	copy(disc[:], data[:discriminatorSize])

	if disc != BridgeInstructionDiscriminator {
		return nil, fmt.Errorf("discriminator mismatch: expected %v, got %v",
			BridgeInstructionDiscriminator, disc)
	}

	cursor := discriminatorSize

	readU8 := func(label string) (uint8, error) {
		if cursor+u8Size > len(data) {
			return 0, fmt.Errorf("not enough bytes to read %s at offset %d", label, cursor)
		}

		v := data[cursor]
		cursor += u8Size

		return v, nil
	}

	readU32 := func(label string) (uint32, error) {
		if cursor+u32Size > len(data) {
			return 0, fmt.Errorf("not enough bytes to read %s at offset %d", label, cursor)
		}

		v := binary.LittleEndian.Uint32(data[cursor:])
		cursor += u32Size

		return v, nil
	}

	readU64 := func(label string) (uint64, error) {
		if cursor+u64Size > len(data) {
			return 0, fmt.Errorf("not enough bytes to read %s at offset %d", label, cursor)
		}

		v := binary.LittleEndian.Uint64(data[cursor:])
		cursor += u64Size

		return v, nil
	}

	readPubkey := func(label string) (solana.PublicKey, error) {
		if cursor+pubkeySize > len(data) {
			return solana.PublicKey{}, fmt.Errorf("not enough bytes to read %s at offset %d", label, cursor)
		}

		var pk solana.PublicKey

		copy(pk[:], data[cursor:cursor+pubkeySize])
		cursor += pubkeySize

		return pk, nil
	}

	// ── transfers: vec<TransferItem> ──────────────────────────────────────────

	transfersLen, err := readU32("transfers.len")
	if err != nil {
		return nil, err
	}

	requiredForTransfers := int(transfersLen) * transferItemSize
	if cursor+requiredForTransfers > len(data) {
		return nil, fmt.Errorf(
			"transfers_len=%d claims %d bytes but only %d remain at offset %d",
			transfersLen, requiredForTransfers, len(data)-cursor, cursor)
	}

	type rawTransfer struct {
		recipient solana.PublicKey
		mintIndex uint8
		amount    uint64
	}

	rawTransfers := make([]rawTransfer, 0, transfersLen)

	for i := uint32(0); i < transfersLen; i++ {
		recipient, err := readPubkey(fmt.Sprintf("transfers[%d].recipient", i))
		if err != nil {
			return nil, err
		}

		mintIndex, err := readU8(fmt.Sprintf("transfers[%d].mint_index", i))
		if err != nil {
			return nil, err
		}

		amount, err := readU64(fmt.Sprintf("transfers[%d].amount", i))
		if err != nil {
			return nil, err
		}

		rawTransfers = append(rawTransfers, rawTransfer{recipient, mintIndex, amount})
	}

	// ── mints: vec<pubkey> ────────────────────────────────────────────────────
	//
	// Deduplicated: mints_len <= transfers_len is valid — multiple transfers
	// may reference the same mint via mint_index.

	mintsLen, err := readU32("mints.len")
	if err != nil {
		return nil, err
	}

	if mintsLen == 0 {
		return nil, fmt.Errorf("mints vec is empty")
	}

	if mintsLen > transfersLen {
		return nil, fmt.Errorf(
			"mints_len=%d cannot exceed transfers_len=%d (every mint must be referenced)",
			mintsLen, transfersLen)
	}

	requiredForMints := int(mintsLen) * pubkeySize
	if cursor+requiredForMints > len(data) {
		return nil, fmt.Errorf(
			"mints_len=%d claims %d bytes but only %d remain at offset %d",
			mintsLen, requiredForMints, len(data)-cursor, cursor)
	}

	mints := make([]solana.PublicKey, 0, mintsLen)

	for i := uint32(0); i < mintsLen; i++ {
		pk, err := readPubkey(fmt.Sprintf("mints[%d]", i))
		if err != nil {
			return nil, err
		}

		mints = append(mints, pk)
	}

	// ── batch_id: u64 ─────────────────────────────────────────────────────────

	batchID, err := readU64("batch_id")
	if err != nil {
		return nil, err
	}

	if cursor != len(data) {
		return nil, fmt.Errorf(
			"parsed successfully but %d unexpected trailing bytes remain at offset %d — "+
				"possible layout mismatch",
			len(data)-cursor, cursor)
	}

	// ── assemble domain types ─────────────────────────────────────────────────
	//
	// Resolve each transfer's mint via mint_index into the deduplicated mints vec.

	receivers := make([]sendtx.BridgingTxReceiver, 0, transfersLen)

	for i, t := range rawTransfers {
		if int(t.mintIndex) >= len(mints) {
			return nil, fmt.Errorf(
				"transfers[%d].mint_index=%d out of bounds (mints_len=%d)",
				i, t.mintIndex, mintsLen)
		}

		receivers = append(receivers, sendtx.BridgingTxReceiver{
			Address: t.recipient.String(),
			TokenAmount: wallet.TokenAmount{
				TokenMint: mints[t.mintIndex].String(),
				Amount:    new(big.Int).SetUint64(t.amount),
			},
		})
	}

	return &BridgeInstructionArgs{
		Receivers: receivers,
		BatchID:   batchID,
	}, nil
}
