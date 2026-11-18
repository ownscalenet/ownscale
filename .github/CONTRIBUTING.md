# Contributing

PRs welcome! Please open an issue first and explain the problem.
For adding or changing a feature, please open an issue to discuss it
before spending time writing the code.

Code conventions for documentation, errors, tests, etc. are in
[AGENTS.md](AGENTS.md); they apply to humans too.

## Local development stack
The repository ships a Nix flake that provides every tool needed to build,
test and run ownscale, so the only things you install yourself are Nix
(with flakes enabled) and a Docker daemon. Run `nix develop` in the
repository root to enter the dev shell.

Day to day commands used for development:
```bash
make ci                      # formatters, linters, build, vet, unit tests
make dev-up BUILD=1 DETACH=1 # rebuild after code changes, run in the background
make logs                    # follow the container logs
make e2e                     # full end-to-end suites against the dev stack
make purge                   # wipe the stack's data (keeps the CA) for a fresh start
make load-cert               # trusts the CA in Firefox to have HTTPS on ownscale.test
```

`/etc/hosts` records are needed for running the e2e tests on local:
```bash
echo "
127.0.0.1 ownscale.test console.ownscale.test dex.ownscale.test
127.0.0.1 aaaa.ownscale.test bbbb.ownscale.test cccc.ownscale.test dddd.ownscale.test eeee.ownscale.test
127.0.0.1 ffff.ownscale.test gggg.ownscale.test hhhh.ownscale.test iiii.ownscale.test jjjj.ownscale.test
127.0.0.1 web1-aaaa.ownscale.test web2-aaaa.ownscale.test web1-bbbb.ownscale.test web2-bbbb.ownscale.test
127.0.0.1 web1-cccc.ownscale.test web2-cccc.ownscale.test" | sudo tee -a /etc/hosts
```

## Commit messages

Commits follow the Go project's style: a short subject prefixed by the area
it touches, and a body that explains why. For example:

```
package: short explanation of what was done

Long explanation of what was done and the reasoning behind the change.
Fix #123
```

