# TT-MCK001: Test-only API used outside a test

Mock setters (`mockExec`, `mockFetch`, `mockReadFile`, …) and assertion helpers (`assertEq`, `assertNe`, `check`, `fail`, `skip`) can only be called from inside a `test "..." { ... }` block. The checker rejects them in regular `func` bodies because they have no implementation outside the test harness.

## Repair

Move the call into a test:

```tartalo
test "fetch is mocked" {
  mockFetch("https://example.com", Response{
    status: 200, ok: true, body: "ok", headers: ""
  })
  let r: Response = fetch("https://example.com")
  assertEq(r.body, "ok")
}
```

For runtime checks you need in production code, use `exit(code)` after `eprint(...)` instead of `fail()`.
