# scalp-assist

Personal futures screener for Binance and Bybit. The implementation follows the project documentation in `specs/`.

## Stage 0 commands

Linux/macOS/Git Bash:

```bash
make test
make build
make run
```

PowerShell equivalents:

```powershell
go test ./...
go build -o screener.exe ./cmd/screener
go run ./cmd/screener -config config.example.yaml
```

Production config should be copied from `config.example.yaml` to `config.yaml`; local runtime files are ignored by Git.
