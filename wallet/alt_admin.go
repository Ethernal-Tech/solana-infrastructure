package wallet

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/gagliardetto/solana-go"
)

// AddressLookupTableProgramID is the on-chain program ID of Solana's native
// Address Lookup Table program.
var AddressLookupTableProgramID = solana.MustPublicKeyFromBase58(
	"AddressLookupTab1e1111111111111111111111111",
)

// ALT program instruction discriminators (bincode u32 enum variant index).
const (
	altIxCreate     uint32 = 0
	altIxFreeze     uint32 = 1
	altIxExtend     uint32 = 2
	altIxDeactivate uint32 = 3
	altIxClose      uint32 = 4

	// ExtendLookupTableMaxPerIx bounds how many addresses we pack into a
	// single ExtendLookupTable instruction. Each pubkey is 32 bytes and the
	// transaction size limit is 1232 bytes; keeping this at 20 leaves room
	// for the signature, message header, static account keys, and the
	// instruction metadata itself.
	ExtendLookupTableMaxPerIx = 20
)

// DeriveAddressLookupTableAddress mirrors the Solana runtime's ALT address
// derivation: PDA of [authority, recent_slot_le_bytes] under the ALT program.
// The returned bump is what must be passed in the create instruction data.
func DeriveAddressLookupTableAddress(
	authority solana.PublicKey, recentSlot uint64,
) (solana.PublicKey, uint8, error) {
	slotBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(slotBytes, recentSlot)

	return solana.FindProgramAddress(
		[][]byte{authority[:], slotBytes},
		AddressLookupTableProgramID,
	)
}

// ALTAdmin builds Address Lookup Table program instructions.
//
// Create / Freeze / Deactivate / Close do not need network access. Extend
// fetches the current ALT state first so it can skip addresses that are
// already stored in the table (this is the "no-op if already present"
// behavior) and chunks the remainder across multiple instructions if needed.
//
// A nil fetcher is allowed; Extend will return an error in that case.
type ALTAdmin struct {
	fetcher IAddressLookupTableFetcher
}

func NewALTAdmin(fetcher IAddressLookupTableFetcher) *ALTAdmin {
	return &ALTAdmin{fetcher: fetcher}
}

// NewCreateInstruction builds the CreateLookupTable instruction and returns
// the derived ALT address. The caller is responsible for submitting the
// instruction in a transaction signed by `authority` and paid by `payer`.
//
// `recentSlot` must be a finalized slot that is within the last 128 slots at
// the time the transaction lands (the runtime verifies this).
func (a *ALTAdmin) NewCreateInstruction(
	authority, payer solana.PublicKey, recentSlot uint64,
) (solana.Instruction, solana.PublicKey, error) {
	altAddress, bump, err := DeriveAddressLookupTableAddress(authority, recentSlot)
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("failed to derive ALT address: %w", err)
	}

	data := make([]byte, 0, 4+8+1)
	data = binary.LittleEndian.AppendUint32(data, altIxCreate)
	data = binary.LittleEndian.AppendUint64(data, recentSlot)
	data = append(data, bump)

	accounts := solana.AccountMetaSlice{
		solana.NewAccountMeta(altAddress, true, false),
		solana.NewAccountMeta(authority, false, true),
		solana.NewAccountMeta(payer, true, true),
		solana.NewAccountMeta(solana.SystemProgramID, false, false),
	}

	return solana.NewInstruction(AddressLookupTableProgramID, accounts, data), altAddress, nil
}

// NewFreezeInstruction builds the FreezeLookupTable instruction. Once frozen
// an ALT can never be extended, deactivated, or closed.
func (a *ALTAdmin) NewFreezeInstruction(
	altAddress, authority solana.PublicKey,
) solana.Instruction {
	data := binary.LittleEndian.AppendUint32(nil, altIxFreeze)

	accounts := solana.AccountMetaSlice{
		solana.NewAccountMeta(altAddress, true, false),
		solana.NewAccountMeta(authority, false, true),
	}

	return solana.NewInstruction(AddressLookupTableProgramID, accounts, data)
}

// NewDeactivateInstruction builds the DeactivateLookupTable instruction.
// Deactivation starts a ~512-slot cooldown; after it elapses the ALT can be
// closed with NewCloseInstruction.
func (a *ALTAdmin) NewDeactivateInstruction(
	altAddress, authority solana.PublicKey,
) solana.Instruction {
	data := binary.LittleEndian.AppendUint32(nil, altIxDeactivate)

	accounts := solana.AccountMetaSlice{
		solana.NewAccountMeta(altAddress, true, false),
		solana.NewAccountMeta(authority, false, true),
	}

	return solana.NewInstruction(AddressLookupTableProgramID, accounts, data)
}

// NewCloseInstruction builds the CloseLookupTable instruction, refunding the
// ALT's rent to `recipient`. The ALT must already be deactivated.
func (a *ALTAdmin) NewCloseInstruction(
	altAddress, authority, recipient solana.PublicKey,
) solana.Instruction {
	data := binary.LittleEndian.AppendUint32(nil, altIxClose)

	accounts := solana.AccountMetaSlice{
		solana.NewAccountMeta(altAddress, true, false),
		solana.NewAccountMeta(authority, false, true),
		solana.NewAccountMeta(recipient, true, false),
	}

	return solana.NewInstruction(AddressLookupTableProgramID, accounts, data)
}

// NewExtendInstructions fetches the current ALT state and returns one or
// more ExtendLookupTable instructions containing only addresses that are not
// already present in the table. Duplicates inside `addresses` itself are also
// removed while preserving first-seen order.
//
// If every requested address is already in the ALT, the returned slice is
// empty and the caller should skip submitting an extend transaction.
//
// When more than ExtendLookupTableMaxPerIx new addresses need to be added,
// they are split across multiple instructions. All returned instructions can
// typically be packed into a single transaction, but very large extend
// batches may need to be split across several transactions by the caller.
func (a *ALTAdmin) NewExtendInstructions(
	ctx context.Context,
	altAddress, authority, payer solana.PublicKey,
	addresses []solana.PublicKey,
) ([]solana.Instruction, error) {
	if a.fetcher == nil {
		return nil, fmt.Errorf(
			"ALTAdmin was constructed without a fetcher; extend requires one")
	}

	state, err := a.fetcher.GetAddressLookupTable(ctx, altAddress)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to fetch address lookup table %s: %w", altAddress.String(), err)
	}

	seen := make(map[solana.PublicKey]struct{})

	if state != nil {
		for _, entry := range state.Addresses {
			seen[entry] = struct{}{}
		}
	}

	toAdd := make([]solana.PublicKey, 0, len(addresses))

	for _, key := range addresses {
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		toAdd = append(toAdd, key)
	}

	if len(toAdd) == 0 {
		return nil, nil
	}

	chunks := (len(toAdd) + ExtendLookupTableMaxPerIx - 1) / ExtendLookupTableMaxPerIx
	instructions := make([]solana.Instruction, 0, chunks)

	for start := 0; start < len(toAdd); start += ExtendLookupTableMaxPerIx {
		end := start + ExtendLookupTableMaxPerIx
		if end > len(toAdd) {
			end = len(toAdd)
		}

		instructions = append(instructions,
			buildExtendLookupTableInstruction(altAddress, authority, payer, toAdd[start:end]))
	}

	return instructions, nil
}

func buildExtendLookupTableInstruction(
	altAddress, authority, payer solana.PublicKey,
	addresses []solana.PublicKey,
) solana.Instruction {
	const pubkeyLen = 32

	data := make([]byte, 0, 4+8+len(addresses)*pubkeyLen)
	data = binary.LittleEndian.AppendUint32(data, altIxExtend)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(addresses)))

	for _, key := range addresses {
		data = append(data, key[:]...)
	}

	accounts := solana.AccountMetaSlice{
		solana.NewAccountMeta(altAddress, true, false),
		solana.NewAccountMeta(authority, false, true),
		solana.NewAccountMeta(payer, true, true),
		solana.NewAccountMeta(solana.SystemProgramID, false, false),
	}

	return solana.NewInstruction(AddressLookupTableProgramID, accounts, data)
}
