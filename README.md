# Arandu ViaCEP

The address of a Brazilian postcode, for a form that asks for the CEP and the
number and fills the rest: the street, the district, the city and the state.

It asks [ViaCEP](https://viacep.com.br) first and
[BrasilAPI](https://brasilapi.com.br) when ViaCEP does not know the CEP or does
not answer, and keeps what it learned in memory for a day.

```sh
go get github.com/hyz-is/arandu-viacep@latest
```

## Wiring

In `bootstrap/app.go`:

```go
postcodes, err := viacep.New(viacep.Config{}) // GET /cep/{cep}
if err != nil {
	return err
}
kernel.Register(postcodes)
```

It owns no table, so there is nothing to migrate.

## The route

`GET /cep/{cep}` answers the address as JSON. The route is public, because a
checkout's buyer has no session and a postcode's address is public data; put
the application's rate limiter in front of it, as in front of every public
route.

| status | body |
| --- | --- |
| 200 | `{"cep":"01310100","street":"Avenida Paulista","district":"Bela Vista","city":"São Paulo","state":"SP","ibge":"3550308"}` |
| 404 | `{"error":"cep_not_found"}` |
| 422 | `{"error":"invalid_cep"}` |
| 503 | `{"error":"cep_unavailable"}` |

## In Go

```go
address, err := postcodes.Lookup(ctx, "01310-100")
```

`viacep.Normalize` reads a CEP written as `00000-000`, `00000000` or with spaces.
An address found is a suggestion for a person to confirm: a CEP for a whole
small town names no street, and a new CEP reaches the bases late.

## License

MIT. See [LICENSE.md](LICENSE.md).
