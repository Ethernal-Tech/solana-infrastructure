# solana-infrastructure
Solana infrastructure library supporting wallet operations, transaction construction, and submission.


# Solana Program Go Bindings

The `solana_program` Go bindings are generated from the `sendtx/build` directory.

The `build` directory must contain the following files:

- `skyline_program-keypair.json`
- `skyline_program.json`
- `skyline_program.so`

---

## Install anchor-go

Install `anchor-go` before generating the bindings:

```bash
go install github.com/gagliardetto/anchor-go@latest
```

## Generate Go Bindings

Run the following command:

```bash
anchor-go --idl $(pwd)/sendtx/build/skyline_program.json \
          --output $(pwd)/sendtx/skyline_program \
          --program-id <PROGRAM_ID>
```

After generating the bindings, remove the `go.mod` and `go.sum` files from the skyline_program folder.
