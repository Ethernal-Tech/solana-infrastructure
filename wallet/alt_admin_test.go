package wallet

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	alt "github.com/gagliardetto/solana-go/programs/address-lookup-table"
	"github.com/stretchr/testify/require"
)

func TestDeriveAddressLookupTableAddress_Deterministic(t *testing.T) {
	authority := solana.NewWallet().PublicKey()

	a1, b1, err := DeriveAddressLookupTableAddress(authority, 12345)
	require.NoError(t, err)

	a2, b2, err := DeriveAddressLookupTableAddress(authority, 12345)
	require.NoError(t, err)

	require.Equal(t, a1, a2)
	require.Equal(t, b1, b2)

	aDiff, _, err := DeriveAddressLookupTableAddress(authority, 12346)
	require.NoError(t, err)
	require.NotEqual(t, a1, aDiff, "different slots should yield different ALT addresses")
}

func TestALTAdmin_NewCreateInstruction(t *testing.T) {
	admin := NewALTAdmin(nil)

	const recentSlot uint64 = 9_876_543

	authority := solana.NewWallet().PublicKey()
	payer := solana.NewWallet().PublicKey()

	ix, altAddress, err := admin.NewCreateInstruction(authority, payer, recentSlot)
	require.NoError(t, err)

	expectedALT, bump, err := DeriveAddressLookupTableAddress(authority, recentSlot)
	require.NoError(t, err)
	require.Equal(t, expectedALT, altAddress)

	require.Equal(t, AddressLookupTableProgramID, ix.ProgramID())

	data, err := ix.Data()
	require.NoError(t, err)

	expected := make([]byte, 0, 13)
	expected = binary.LittleEndian.AppendUint32(expected, altIxCreate)
	expected = binary.LittleEndian.AppendUint64(expected, recentSlot)
	expected = append(expected, bump)
	require.Equal(t, expected, data)

	accounts := ix.Accounts()
	require.Len(t, accounts, 4)

	require.Equal(t, altAddress, accounts[0].PublicKey)
	require.True(t, accounts[0].IsWritable)
	require.False(t, accounts[0].IsSigner)

	require.Equal(t, authority, accounts[1].PublicKey)
	require.False(t, accounts[1].IsWritable)
	require.True(t, accounts[1].IsSigner)

	require.Equal(t, payer, accounts[2].PublicKey)
	require.True(t, accounts[2].IsWritable)
	require.True(t, accounts[2].IsSigner)

	require.Equal(t, solana.SystemProgramID, accounts[3].PublicKey)
	require.False(t, accounts[3].IsWritable)
	require.False(t, accounts[3].IsSigner)
}

func TestALTAdmin_SimpleDiscriminatorInstructions(t *testing.T) {
	admin := NewALTAdmin(nil)

	altAddress := solana.NewWallet().PublicKey()
	authority := solana.NewWallet().PublicKey()
	recipient := solana.NewWallet().PublicKey()

	cases := []struct {
		name      string
		ix        solana.Instruction
		disc      uint32
		wantMetas []*solana.AccountMeta
	}{
		{
			name: "freeze",
			ix:   admin.NewFreezeInstruction(altAddress, authority),
			disc: altIxFreeze,
			wantMetas: []*solana.AccountMeta{
				solana.NewAccountMeta(altAddress, true, false),
				solana.NewAccountMeta(authority, false, true),
			},
		},
		{
			name: "deactivate",
			ix:   admin.NewDeactivateInstruction(altAddress, authority),
			disc: altIxDeactivate,
			wantMetas: []*solana.AccountMeta{
				solana.NewAccountMeta(altAddress, true, false),
				solana.NewAccountMeta(authority, false, true),
			},
		},
		{
			name: "close",
			ix:   admin.NewCloseInstruction(altAddress, authority, recipient),
			disc: altIxClose,
			wantMetas: []*solana.AccountMeta{
				solana.NewAccountMeta(altAddress, true, false),
				solana.NewAccountMeta(authority, false, true),
				solana.NewAccountMeta(recipient, true, false),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, AddressLookupTableProgramID, tc.ix.ProgramID())

			data, err := tc.ix.Data()
			require.NoError(t, err)
			require.Equal(t, binary.LittleEndian.AppendUint32(nil, tc.disc), data)

			require.Equal(t, tc.wantMetas, tc.ix.Accounts())
		})
	}
}

func TestALTAdmin_NewExtendInstructions_OnlyMissing(t *testing.T) {
	ctx := context.Background()

	altAddress := solana.NewWallet().PublicKey()
	authority := solana.NewWallet().PublicKey()
	payer := solana.NewWallet().PublicKey()

	existing := randomKeys(t, 3)
	requested := append(solana.PublicKeySlice{}, existing...)

	newKeys := randomKeys(t, 2)
	requested = append(requested, newKeys...)
	requested = append(requested, newKeys[0])

	fetcher := newFakeALTFetcher()
	fetcher.tables[altAddress] = &alt.AddressLookupTableState{Addresses: existing}

	admin := NewALTAdmin(fetcher)

	ixs, err := admin.NewExtendInstructions(ctx, altAddress, authority, payer, requested)
	require.NoError(t, err)
	require.Len(t, ixs, 1)

	data, err := ixs[0].Data()
	require.NoError(t, err)

	got := parseExtendInstructionData(t, data)
	require.Equal(t, newKeys, got,
		"only addresses missing from the ALT should be included, preserving order and deduping")

	accounts := ixs[0].Accounts()
	require.Len(t, accounts, 4)
	require.Equal(t, altAddress, accounts[0].PublicKey)
	require.Equal(t, authority, accounts[1].PublicKey)
	require.Equal(t, payer, accounts[2].PublicKey)
	require.Equal(t, solana.SystemProgramID, accounts[3].PublicKey)
}

func TestALTAdmin_NewExtendInstructions_NoopWhenAllPresent(t *testing.T) {
	ctx := context.Background()

	altAddress := solana.NewWallet().PublicKey()
	authority := solana.NewWallet().PublicKey()
	payer := solana.NewWallet().PublicKey()

	existing := randomKeys(t, 4)

	fetcher := newFakeALTFetcher()
	fetcher.tables[altAddress] = &alt.AddressLookupTableState{Addresses: existing}

	admin := NewALTAdmin(fetcher)

	ixs, err := admin.NewExtendInstructions(
		ctx, altAddress, authority, payer, existing)
	require.NoError(t, err)
	require.Empty(t, ixs)
}

func TestALTAdmin_NewExtendInstructions_ChunksLargeBatch(t *testing.T) {
	ctx := context.Background()

	altAddress := solana.NewWallet().PublicKey()
	authority := solana.NewWallet().PublicKey()
	payer := solana.NewWallet().PublicKey()

	fetcher := newFakeALTFetcher()
	fetcher.tables[altAddress] = &alt.AddressLookupTableState{}

	totalNew := ExtendLookupTableMaxPerIx*2 + 3
	newKeys := randomKeys(t, totalNew)

	admin := NewALTAdmin(fetcher)

	ixs, err := admin.NewExtendInstructions(
		ctx, altAddress, authority, payer, newKeys)
	require.NoError(t, err)
	require.Len(t, ixs, 3)

	var reconstructed solana.PublicKeySlice

	for i, ix := range ixs {
		data, err := ix.Data()
		require.NoError(t, err)

		parsed := parseExtendInstructionData(t, data)

		switch i {
		case 0, 1:
			require.Len(t, parsed, ExtendLookupTableMaxPerIx)
		case 2:
			require.Len(t, parsed, 3)
		}

		reconstructed = append(reconstructed, parsed...)
	}

	require.Equal(t, newKeys, reconstructed)
}

func TestALTAdmin_NewExtendInstructions_WithoutFetcher(t *testing.T) {
	admin := NewALTAdmin(nil)

	_, err := admin.NewExtendInstructions(
		context.Background(),
		solana.NewWallet().PublicKey(),
		solana.NewWallet().PublicKey(),
		solana.NewWallet().PublicKey(),
		randomKeys(t, 2),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetcher")
}

func TestALTAdmin_NewExtendInstructions_FetcherError(t *testing.T) {
	fetcher := newFakeALTFetcher()
	fetcher.err = errors.New("rpc down")

	admin := NewALTAdmin(fetcher)

	_, err := admin.NewExtendInstructions(
		context.Background(),
		solana.NewWallet().PublicKey(),
		solana.NewWallet().PublicKey(),
		solana.NewWallet().PublicKey(),
		randomKeys(t, 2),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "rpc down")
}

func parseExtendInstructionData(t *testing.T, data []byte) solana.PublicKeySlice {
	t.Helper()

	require.GreaterOrEqual(t, len(data), 12, "extend data must have discriminator + length prefix")
	require.Equal(t, altIxExtend, binary.LittleEndian.Uint32(data[:4]))

	count := binary.LittleEndian.Uint64(data[4:12])
	require.Equal(t, 12+int(count)*32, len(data), "data length must match pubkey count")

	out := make(solana.PublicKeySlice, count)
	for i := uint64(0); i < count; i++ {
		copy(out[i][:], data[12+int(i)*32:12+int(i+1)*32])
	}

	return out
}
