# Redacted Windows CI diagnostics

Generated from selected failed GitHub Actions jobs. Only bounded failure contexts and log tails are included.

## PR 176 · Go check (windows-latest)

```text
2026-10-21T04:23:29.7840259Z 
2026-10-21T04:23:29.7840624Z ##[endgroup]
2026-10-21T04:23:29.8180103Z ##[group]Run unformatted="$(git ls-files '*.go' | while IFS= read -r file; do gofmt -l "$file"; done)"
2026-10-21T04:23:29.8180940Z unformatted="$(git ls-files '*.go' | while IFS= read -r file; do gofmt -l "$file"; done)"
2026-10-21T04:23:29.8181432Z if [ -n "$unformatted" ]; then
2026-10-21T04:23:29.8181790Z   printf 'Unformatted Go files:\n%s\n' "$unformatted"
2026-10-21T04:23:29.8182141Z   exit 1
2026-10-21T04:23:29.8182328Z fi
2026-10-21T04:23:29.8214605Z shell: C:\Program Files\Git\bin\bash.EXE --noprofile --norc -e -o pipefail {0}
2026-10-21T04:23:29.8215328Z env:
2026-10-21T04:23:29.8215685Z   GOTOOLCHAIN: local
2026-10-21T04:23:29.8216040Z ##[endgroup]
2026-10-21T04:24:08.4901672Z ##[group]Run go test ./internal/app -run '^TestExecutionTwentyConcurrentCallsPerConversation$' -count=5 -timeout=150s
2026-10-21T04:24:08.4902661Z go test ./internal/app -run '^TestExecutionTwentyConcurrentCallsPerConversation$' -count=5 -timeout=150s
2026-10-21T04:24:08.5513799Z shell: C:\Program Files\PowerShell\7\pwsh.EXE -command ". '{0}'"
2026-10-21T04:24:08.5514258Z env:
2026-10-21T04:24:08.5514499Z   GOTOOLCHAIN: local
2026-10-21T04:24:08.5514786Z ##[endgroup]
2026-10-21T04:24:39.3938835Z ok  	github.com/uvwt/agentdock/internal/app	19.125s
2026-10-21T04:24:39.8058798Z ##[group]Run go test -p 2 ./... -count=1 -timeout=8m
2026-10-21T04:24:39.8059476Z go test -p 2 ./... -count=1 -timeout=8m
2026-10-21T04:24:39.8141316Z shell: C:\Program Files\PowerShell\7\pwsh.EXE -command ". '{0}'"
2026-10-21T04:24:39.8141693Z env:
2026-10-21T04:24:39.8141885Z   GOTOOLCHAIN: local
2026-10-21T04:24:39.8142100Z ##[endgroup]
2026-10-21T04:24:49.1568936Z ok  	github.com/uvwt/agentdock/cmd/agentdock	0.551s
2026-10-21T04:24:49.1569906Z ok  	github.com/uvwt/agentdock/cmd/agentdock/internal/logx	0.074s
2026-10-21T04:24:49.1570797Z ?   	github.com/uvwt/agentdock/cmd/agentdock-arbiter	[no test files]
2026-10-21T04:24:49.1571657Z ok  	github.com/uvwt/agentdock/cmd/agentdock-shim	0.096s
2026-10-21T04:24:49.1572450Z ?   	github.com/uvwt/agentdock/cmd/agentdock-tray	[no test files]
2026-10-21T04:24:50.0183121Z ok  	github.com/uvwt/agentdock/desktop/windows/tray	0.042s
2026-10-21T04:24:54.2181105Z ok  	github.com/uvwt/agentdock/internal/acp	4.029s
2026-10-21T04:25:03.2562445Z ok  	github.com/uvwt/agentdock/internal/activity	11.846s
2026-10-21T04:25:03.2564714Z ok  	github.com/uvwt/agentdock/internal/agentinstructions	0.158s
2026-10-21T04:25:03.2565552Z ok  	github.com/uvwt/agentdock/internal/androidbridge	0.027s
2026-10-21T04:30:58.7338418Z ok  	github.com/uvwt/agentdock/internal/app	362.816s
2026-10-21T04:30:58.7339676Z ok  	github.com/uvwt/agentdock/internal/auth	7.546s
2026-10-21T04:30:58.7340476Z ok  	github.com/uvwt/agentdock/internal/buildinfo	0.021s
2026-10-21T04:30:58.7341341Z ok  	github.com/uvwt/agentdock/internal/capabilityrouting	0.026s
2026-10-21T04:30:58.7342252Z ok  	github.com/uvwt/agentdock/internal/commandcompletion	2.384s
2026-10-21T04:30:58.7343080Z ok  	github.com/uvwt/agentdock/internal/config	1.856s
2026-10-21T04:30:58.7344010Z ?   	github.com/uvwt/agentdock/internal/contextguide	[no test files]
2026-10-21T04:30:58.7344944Z ok  	github.com/uvwt/agentdock/internal/desktopcontrol	0.061s
2026-10-21T04:30:58.7345954Z ok  	github.com/uvwt/agentdock/internal/desktopruntime	23.555s
2026-10-21T04:30:58.7346797Z ok  	github.com/uvwt/agentdock/internal/envstore	3.929s
2026-10-21T04:30:58.7347639Z ok  	github.com/uvwt/agentdock/internal/evolution	3.545s
2026-10-21T04:30:58.7348437Z ok  	github.com/uvwt/agentdock/internal/executioncompat	0.039s
2026-10-21T04:30:58.7349259Z ok  	github.com/uvwt/agentdock/internal/externalagent	0.038s
2026-10-21T04:30:58.7350115Z ok  	github.com/uvwt/agentdock/internal/fs/atomicfile	1.441s
2026-10-21T04:30:58.7350929Z ok  	github.com/uvwt/agentdock/internal/fs/filelock	0.910s
2026-10-21T04:30:58.7351762Z ok  	github.com/uvwt/agentdock/internal/fs/processlock	0.085s
2026-10-21T04:30:58.7352523Z ok  	github.com/uvwt/agentdock/internal/fs/securepath	0.069s
2026-10-21T04:30:58.7353377Z ok  	github.com/uvwt/agentdock/internal/generationretention	0.159s
2026-10-21T04:30:58.7354243Z ok  	github.com/uvwt/agentdock/internal/httpx	41.240s
2026-10-21T04:30:58.7355037Z ok  	github.com/uvwt/agentdock/internal/httpx/requestmeta	0.027s
2026-10-21T04:30:58.7355887Z ok  	github.com/uvwt/agentdock/internal/insertion	3.313s
2026-10-21T04:30:58.7356727Z ok  	github.com/uvwt/agentdock/internal/installer	30.937s
2026-10-21T04:30:58.7357481Z ok  	github.com/uvwt/agentdock/internal/logfile	0.040s
2026-10-21T04:30:58.7358174Z ok  	github.com/uvwt/agentdock/internal/mcp	69.649s
2026-10-21T04:30:58.7359226Z ok  	github.com/uvwt/agentdock/internal/mcp/client	6.347s
2026-10-21T04:30:58.7360153Z ?   	github.com/uvwt/agentdock/internal/mcpappcompat	[no test files]
2026-10-21T04:30:58.7361040Z ok  	github.com/uvwt/agentdock/internal/nexusbridge	0.154s
2026-10-21T04:30:58.7361829Z ok  	github.com/uvwt/agentdock/internal/nexusclient	0.036s
2026-10-21T04:30:58.7362583Z ok  	github.com/uvwt/agentdock/internal/permission	5.664s
2026-10-21T04:30:58.7363377Z ok  	github.com/uvwt/agentdock/internal/plugin	28.631s
2026-10-21T04:30:58.7364076Z ok  	github.com/uvwt/agentdock/internal/process	0.155s
2026-10-21T04:30:58.7367330Z ok  	github.com/uvwt/agentdock/internal/publicartifacts	2.102s
2026-10-21T04:30:58.7368301Z ok  	github.com/uvwt/agentdock/internal/requesttrace	0.036s
2026-10-21T04:30:58.7370746Z ok  	github.com/uvwt/agentdock/internal/runtimeapi	0.620s
2026-10-21T04:30:58.7371598Z ok  	github.com/uvwt/agentdock/internal/selfupdate	2.310s
2026-10-21T04:30:58.7372389Z ok  	github.com/uvwt/agentdock/internal/skill	1.288s
2026-10-21T04:30:58.7373220Z ok  	github.com/uvwt/agentdock/internal/skill/bundle	1.118s
2026-10-21T04:30:58.7374104Z ?   	github.com/uvwt/agentdock/internal/skill/packagehash	[no test files]
2026-10-21T04:30:58.7375113Z --- FAIL: TestStoreRecoversInterruptedActivationStages (0.76s)
2026-10-21T04:30:58.7376711Z     --- FAIL: TestStoreRecoversInterruptedActivationStages/candidate_moved (0.49s)
2026-10-21T04:30:58.7379537Z         transaction_test.go:73: SKILL.md = "new", want "old"
2026-10-21T04:30:58.7380212Z FAIL
2026-10-21T04:30:58.7380805Z FAIL	github.com/uvwt/agentdock/internal/skill/state	4.564s
2026-10-21T04:30:58.7381707Z ok  	github.com/uvwt/agentdock/internal/skilldata	0.104s
2026-10-21T04:30:58.7382573Z ok  	github.com/uvwt/agentdock/internal/snapshot	19.635s
2026-10-21T04:30:58.7383372Z ok  	github.com/uvwt/agentdock/internal/taskstate	32.113s
2026-10-21T04:30:58.7384238Z ok  	github.com/uvwt/agentdock/internal/textutil	0.038s
2026-10-21T04:30:58.7384859Z ok  	github.com/uvwt/agentdock/internal/tool/acp	0.062s
2026-10-21T04:30:58.7385468Z ok  	github.com/uvwt/agentdock/internal/tool/androiddevice	0.085s
2026-10-21T04:30:58.7385954Z ok  	github.com/uvwt/agentdock/internal/tool/browser	0.131s
2026-10-21T04:30:58.7386429Z ok  	github.com/uvwt/agentdock/internal/tool/capability	0.033s
2026-10-21T04:30:58.7386949Z ok  	github.com/uvwt/agentdock/internal/tool/command	11.712s
2026-10-21T04:30:58.7387933Z ok  	github.com/uvwt/agentdock/internal/tool/command/session	1.295s
2026-10-21T04:30:58.7388899Z ok  	github.com/uvwt/agentdock/internal/tool/contract	0.038s
2026-10-21T04:30:58.7389729Z ok  	github.com/uvwt/agentdock/internal/tool/core	0.029s
2026-10-21T04:30:58.7950593Z ok  	github.com/uvwt/agentdock/internal/tool/file	3.577s
2026-10-21T04:30:59.9138740Z ok  	github.com/uvwt/agentdock/internal/tool/mcp	0.054s
2026-10-21T04:31:00.3699888Z ok  	github.com/uvwt/agentdock/internal/tool/media	0.454s
2026-10-21T04:31:08.4549783Z ok  	github.com/uvwt/agentdock/internal/tool/plugin	7.708s
2026-10-21T04:31:08.4550652Z ok  	github.com/uvwt/agentdock/internal/tool/recall	0.254s
2026-10-21T04:31:08.4551266Z ok  	github.com/uvwt/agentdock/internal/tool/skill	2.280s
2026-10-21T04:31:08.4552383Z ok  	github.com/uvwt/agentdock/internal/tool/task	1.056s
2026-10-21T04:31:08.4552898Z ?   	github.com/uvwt/agentdock/internal/tool/workspace	[no test files]
2026-10-21T04:31:08.4553448Z ok  	github.com/uvwt/agentdock/internal/updateengine	0.884s
2026-10-21T04:31:08.4553972Z ?   	github.com/uvwt/agentdock/internal/updateplatform	[no test files]
2026-10-21T04:31:10.6818220Z ok  	github.com/uvwt/agentdock/internal/workspace	1.582s
2026-10-21T04:31:10.6819780Z ?   	github.com/uvwt/agentdock/scripts/ci/parallel-macos/fixture-core	[no test files]
2026-10-21T04:31:27.2770116Z ok  	github.com/uvwt/agentdock/scripts/test	16.690s
2026-10-21T04:31:27.2771424Z ok  	github.com/uvwt/agentdock/tools/release	0.117s
2026-10-21T04:31:27.2772099Z FAIL
2026-10-21T04:31:27.8339380Z ##[error]Process completed with exit code 1.
2026-10-21T04:31:27.8695395Z Post job cleanup.
2026-10-21T04:31:28.0723784Z [command]"C:\Program Files\Git\bin\git.exe" version
2026-10-21T04:31:28.1113187Z git version 2.56.0.windows.2
2026-10-21T04:31:28.1196632Z Temporarily overriding HOME='D:\a\_temp\0f894160-59a0-4826-b939-2df0d086705b' before making global git config changes
2026-10-21T04:31:28.1198243Z Adding repository directory to the temporary git global config as a safe directory
2026-10-21T04:31:28.1209043Z [command]"C:\Program Files\Git\bin\git.exe" config --global --add safe.directory D:\a\AgentDock-Workbench\AgentDock-Workbench
2026-10-21T04:31:28.1718381Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp core\.sshCommand
2026-10-21T04:31:28.2134502Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'core\.sshCommand' && git config --local --unset-all 'core.sshCommand' || :\""
2026-10-21T04:31:28.7768522Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp http\.https\:\/\/github\.com\/\.extraheader
2026-10-21T04:31:28.8200071Z http.https://github.com/.extraheader
2026-10-21T04:31:28.8255320Z [command]"C:\Program Files\Git\bin\git.exe" config --local --unset-all http.https://github.com/.extraheader
2026-10-21T04:31:28.8651932Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'http\.https\:\/\/github\.com\/\.extraheader' && git config --local --unset-all 'http.https://github.com/.extraheader' || :\""
2026-10-21T04:31:29.4297038Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp ^includeIf\.gitdir:
2026-10-21T04:31:29.4671761Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "git config --local --show-origin --name-only --get-regexp remote.origin.url"
2026-10-21T04:31:30.0646585Z Cleaning up orphan processes
```

## PR 175 · Go check (windows-latest)

```text
2026-10-21T04:10:20.5664155Z 
2026-10-21T04:10:20.5664616Z ##[endgroup]
2026-10-21T04:10:20.5990887Z ##[group]Run unformatted="$(git ls-files '*.go' | while IFS= read -r file; do gofmt -l "$file"; done)"
2026-10-21T04:10:20.5991749Z unformatted="$(git ls-files '*.go' | while IFS= read -r file; do gofmt -l "$file"; done)"
2026-10-21T04:10:20.5992280Z if [ -n "$unformatted" ]; then
2026-10-21T04:10:20.5992754Z   printf 'Unformatted Go files:\n%s\n' "$unformatted"
2026-10-21T04:10:20.5993154Z   exit 1
2026-10-21T04:10:20.5993566Z fi
2026-10-21T04:10:20.6014129Z shell: C:\Program Files\Git\bin\bash.EXE --noprofile --norc -e -o pipefail {0}
2026-10-21T04:10:20.6014662Z env:
2026-10-21T04:10:20.6015009Z   GOTOOLCHAIN: local
2026-10-21T04:10:20.6015305Z ##[endgroup]
2026-10-21T04:11:00.3784019Z ##[group]Run go test ./internal/app -run '^TestExecutionTwentyConcurrentCallsPerConversation$' -count=5 -timeout=150s
2026-10-21T04:11:00.3784928Z go test ./internal/app -run '^TestExecutionTwentyConcurrentCallsPerConversation$' -count=5 -timeout=150s
2026-10-21T04:11:00.4360605Z shell: C:\Program Files\PowerShell\7\pwsh.EXE -command ". '{0}'"
2026-10-21T04:11:00.4361177Z env:
2026-10-21T04:11:00.4361539Z   GOTOOLCHAIN: local
2026-10-21T04:11:00.4361923Z ##[endgroup]
2026-10-21T04:11:25.7664298Z ok  	github.com/uvwt/agentdock/internal/app	14.234s
2026-10-21T04:11:26.0861600Z ##[group]Run go test -p 2 ./... -count=1 -timeout=8m
2026-10-21T04:11:26.0862571Z go test -p 2 ./... -count=1 -timeout=8m
2026-10-21T04:11:26.0938340Z shell: C:\Program Files\PowerShell\7\pwsh.EXE -command ". '{0}'"
2026-10-21T04:11:26.0938859Z env:
2026-10-21T04:11:26.0939162Z   GOTOOLCHAIN: local
2026-10-21T04:11:26.0939462Z ##[endgroup]
2026-10-21T04:11:34.1731306Z ok  	github.com/uvwt/agentdock/cmd/agentdock	0.490s
2026-10-21T04:11:34.1733691Z ok  	github.com/uvwt/agentdock/cmd/agentdock/internal/logx	0.048s
2026-10-21T04:11:34.1734598Z ?   	github.com/uvwt/agentdock/cmd/agentdock-arbiter	[no test files]
2026-10-21T04:11:34.1736630Z ok  	github.com/uvwt/agentdock/cmd/agentdock-shim	0.072s
2026-10-21T04:11:34.1737442Z ?   	github.com/uvwt/agentdock/cmd/agentdock-tray	[no test files]
2026-10-21T04:11:34.8369168Z ok  	github.com/uvwt/agentdock/desktop/windows/tray	0.033s
2026-10-21T04:11:38.5075856Z ok  	github.com/uvwt/agentdock/internal/acp	2.973s
2026-10-21T04:11:44.2048415Z ok  	github.com/uvwt/agentdock/internal/activity	7.996s
2026-10-21T04:11:44.2049279Z ok  	github.com/uvwt/agentdock/internal/agentinstructions	0.135s
2026-10-21T04:11:44.2050200Z ok  	github.com/uvwt/agentdock/internal/androidbridge	0.046s
2026-10-21T04:16:00.7120693Z ok  	github.com/uvwt/agentdock/internal/app	259.580s
2026-10-21T04:16:00.7122507Z ok  	github.com/uvwt/agentdock/internal/auth	5.084s
2026-10-21T04:16:00.7124032Z ok  	github.com/uvwt/agentdock/internal/buildinfo	0.023s
2026-10-21T04:16:00.7125626Z ok  	github.com/uvwt/agentdock/internal/capabilityrouting	0.019s
2026-10-21T04:16:00.7126561Z ok  	github.com/uvwt/agentdock/internal/commandcompletion	1.551s
2026-10-21T04:16:00.7127329Z ok  	github.com/uvwt/agentdock/internal/config	1.726s
2026-10-21T04:16:00.7128618Z ?   	github.com/uvwt/agentdock/internal/contextguide	[no test files]
2026-10-21T04:16:00.7130141Z ok  	github.com/uvwt/agentdock/internal/desktopcontrol	0.049s
2026-10-21T04:16:00.7131158Z ok  	github.com/uvwt/agentdock/internal/desktopruntime	20.091s
2026-10-21T04:16:00.7131999Z ok  	github.com/uvwt/agentdock/internal/envstore	2.458s
2026-10-21T04:16:00.7132739Z ok  	github.com/uvwt/agentdock/internal/evolution	2.176s
2026-10-21T04:16:00.7133892Z ok  	github.com/uvwt/agentdock/internal/executioncompat	0.039s
2026-10-21T04:16:00.7135355Z ok  	github.com/uvwt/agentdock/internal/externalagent	0.028s
2026-10-21T04:16:00.7136183Z ok  	github.com/uvwt/agentdock/internal/fs/atomicfile	1.205s
2026-10-21T04:16:00.7136971Z ok  	github.com/uvwt/agentdock/internal/fs/filelock	0.720s
2026-10-21T04:16:00.7137773Z ok  	github.com/uvwt/agentdock/internal/fs/processlock	0.080s
2026-10-21T04:16:00.7138607Z ok  	github.com/uvwt/agentdock/internal/fs/securepath	0.061s
2026-10-21T04:16:00.7139950Z ok  	github.com/uvwt/agentdock/internal/generationretention	0.185s
2026-10-21T04:16:00.7141104Z ok  	github.com/uvwt/agentdock/internal/httpx	34.578s
2026-10-21T04:16:00.7142543Z ok  	github.com/uvwt/agentdock/internal/httpx/requestmeta	0.021s
2026-10-21T04:16:00.7143506Z ok  	github.com/uvwt/agentdock/internal/insertion	2.292s
2026-10-21T04:16:00.7144236Z ok  	github.com/uvwt/agentdock/internal/installer	24.723s
2026-10-21T04:16:00.7145127Z ok  	github.com/uvwt/agentdock/internal/logfile	0.037s
2026-10-21T04:16:00.7146306Z ok  	github.com/uvwt/agentdock/internal/mcp	52.120s
2026-10-21T04:16:00.7147007Z ok  	github.com/uvwt/agentdock/internal/mcp/client	5.493s
2026-10-21T04:16:00.7147777Z ?   	github.com/uvwt/agentdock/internal/mcpappcompat	[no test files]
2026-10-21T04:16:00.7148916Z ok  	github.com/uvwt/agentdock/internal/nexusbridge	0.146s
2026-10-21T04:16:00.7149723Z ok  	github.com/uvwt/agentdock/internal/nexusclient	0.031s
2026-10-21T04:16:00.7150545Z ok  	github.com/uvwt/agentdock/internal/permission	3.730s
2026-10-21T04:16:00.7151280Z ok  	github.com/uvwt/agentdock/internal/plugin	22.367s
2026-10-21T04:16:00.7152063Z ok  	github.com/uvwt/agentdock/internal/process	0.133s
2026-10-21T04:16:00.7160102Z ok  	github.com/uvwt/agentdock/internal/publicartifacts	1.337s
2026-10-21T04:16:00.7169922Z ok  	github.com/uvwt/agentdock/internal/requesttrace	0.023s
2026-10-21T04:16:00.7172203Z ok  	github.com/uvwt/agentdock/internal/runtimeapi	0.531s
2026-10-21T04:16:00.7207275Z ok  	github.com/uvwt/agentdock/internal/selfupdate	1.989s
2026-10-21T04:16:00.7219582Z ok  	github.com/uvwt/agentdock/internal/skill	0.989s
2026-10-21T04:16:00.7268153Z ok  	github.com/uvwt/agentdock/internal/skill/bundle	0.777s
2026-10-21T04:16:00.7280548Z ?   	github.com/uvwt/agentdock/internal/skill/packagehash	[no test files]
2026-10-21T04:16:00.7303318Z ok  	github.com/uvwt/agentdock/internal/skill/state	3.112s
2026-10-21T04:16:00.7306727Z ok  	github.com/uvwt/agentdock/internal/skilldata	0.067s
2026-10-21T04:16:00.7308675Z ok  	github.com/uvwt/agentdock/internal/snapshot	15.943s
2026-10-21T04:16:00.7309829Z ok  	github.com/uvwt/agentdock/internal/taskstate	12.065s
2026-10-21T04:16:00.7310811Z ok  	github.com/uvwt/agentdock/internal/textutil	0.022s
2026-10-21T04:16:00.7311731Z ok  	github.com/uvwt/agentdock/internal/tool/acp	0.039s
2026-10-21T04:16:00.7312666Z ok  	github.com/uvwt/agentdock/internal/tool/androiddevice	0.044s
2026-10-21T04:16:00.7313701Z ok  	github.com/uvwt/agentdock/internal/tool/browser	0.114s
2026-10-21T04:16:00.7314726Z ok  	github.com/uvwt/agentdock/internal/tool/capability	0.029s
2026-10-21T04:16:10.7981930Z --- FAIL: TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths (0.00s)
2026-10-21T04:16:10.7985320Z     command_runtime_service_windows_test.go:30: default WSL workdir = "/mnt/c/Users/runneradmin/AppData/Local/Temp/TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths4294218025/001", want bound workspace "/mnt/c/Users/RUNNER~1/AppData/Local/Temp/TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths4294218025/001"
2026-10-21T04:16:10.7987501Z --- FAIL: TestPrepareWSLSkillInvocationUsesPrivateDataDirectory (0.07s)
2026-10-21T04:16:10.7988632Z     --- FAIL: TestPrepareWSLSkillInvocationUsesPrivateDataDirectory/environment_only (0.02s)
2026-10-21T04:16:10.7993421Z         command_runtime_service_windows_test.go:162: command arguments = []string{"--distribution", "Ubuntu", "--cd", "/mnt/c/Users/runneradmin/AppData/Local/Temp/TestPrepareWSLSkillInvocationUsesPrivateDataDirectoryenvironmen3791472880/001", "--exec", "bash", "-lc", "pwd"}, want []string{"--distribution", "Ubuntu", "--cd", "/mnt/c/Users/RUNNER~1/AppData/Local/Temp/TestPrepareWSLSkillInvocationUsesPrivateDataDirectoryenvironmen3791472880/001", "--exec", "bash", "-lc", "pwd"}
2026-10-21T04:16:10.7996100Z FAIL
2026-10-21T04:16:10.7996650Z FAIL	github.com/uvwt/agentdock/internal/tool/command	10.338s
2026-10-21T04:16:10.7997340Z ok  	github.com/uvwt/agentdock/internal/tool/command/session	1.219s
2026-10-21T04:16:10.7998014Z ok  	github.com/uvwt/agentdock/internal/tool/contract	0.044s
2026-10-21T04:16:10.7998629Z ok  	github.com/uvwt/agentdock/internal/tool/core	0.024s
2026-10-21T04:16:10.7999291Z ok  	github.com/uvwt/agentdock/internal/tool/file	2.349s
2026-10-21T04:16:10.7999880Z ok  	github.com/uvwt/agentdock/internal/tool/mcp	0.046s
2026-10-21T04:16:10.8000593Z ok  	github.com/uvwt/agentdock/internal/tool/media	0.366s
2026-10-21T04:16:16.6596406Z ok  	github.com/uvwt/agentdock/internal/tool/plugin	5.742s
2026-10-21T04:16:16.6597248Z ok  	github.com/uvwt/agentdock/internal/tool/recall	0.240s
2026-10-21T04:16:16.6598016Z ok  	github.com/uvwt/agentdock/internal/tool/skill	1.215s
2026-10-21T04:16:16.6598720Z ok  	github.com/uvwt/agentdock/internal/tool/task	1.288s
2026-10-21T04:16:16.6599504Z ?   	github.com/uvwt/agentdock/internal/tool/workspace	[no test files]
2026-10-21T04:16:17.4290320Z ok  	github.com/uvwt/agentdock/internal/updateengine	0.506s
2026-10-21T04:16:17.4291219Z ?   	github.com/uvwt/agentdock/internal/updateplatform	[no test files]
2026-10-21T04:16:18.5778221Z ok  	github.com/uvwt/agentdock/internal/workspace	1.262s
2026-10-21T04:16:18.5779291Z ?   	github.com/uvwt/agentdock/scripts/ci/parallel-macos/fixture-core	[no test files]
2026-10-21T04:16:36.5573579Z ok  	github.com/uvwt/agentdock/scripts/test	17.740s
2026-10-21T04:16:36.5574163Z ok  	github.com/uvwt/agentdock/tools/release	0.090s
2026-10-21T04:16:36.5574705Z FAIL
2026-10-21T04:16:37.1037737Z ##[error]Process completed with exit code 1.
2026-10-21T04:16:37.1417146Z Post job cleanup.
2026-10-21T04:16:37.3407634Z [command]"C:\Program Files\Git\bin\git.exe" version
2026-10-21T04:16:37.3831178Z git version 2.56.0.windows.2
2026-10-21T04:16:37.3915393Z Temporarily overriding HOME='D:\a\_temp\448b6941-23f7-4aaf-9402-86e9442e31e9' before making global git config changes
2026-10-21T04:16:37.3917745Z Adding repository directory to the temporary git global config as a safe directory
2026-10-21T04:16:37.3926815Z [command]"C:\Program Files\Git\bin\git.exe" config --global --add safe.directory D:\a\AgentDock-Workbench\AgentDock-Workbench
2026-10-21T04:16:37.4568839Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp core\.sshCommand
2026-10-21T04:16:37.5120718Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'core\.sshCommand' && git config --local --unset-all 'core.sshCommand' || :\""
2026-10-21T04:16:38.4757751Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp http\.https\:\/\/github\.com\/\.extraheader
2026-10-21T04:16:38.5162950Z http.https://github.com/.extraheader
2026-10-21T04:16:38.5220626Z [command]"C:\Program Files\Git\bin\git.exe" config --local --unset-all http.https://github.com/.extraheader
2026-10-21T04:16:38.5697744Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'http\.https\:\/\/github\.com\/\.extraheader' && git config --local --unset-all 'http.https://github.com/.extraheader' || :\""
2026-10-21T04:16:39.1610279Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp ^includeIf\.gitdir:
2026-10-21T04:16:39.2019465Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "git config --local --show-origin --name-only --get-regexp remote.origin.url"
2026-10-21T04:16:40.3183718Z Cleaning up orphan processes
```

## PR 175 · core (windows-2025)

```text
2026-10-21T04:09:50.7029700Z set GOCACHEPROG=
2026-10-21T04:09:50.7029974Z set GODEBUG=
2026-10-21T04:09:50.7030333Z set GOENV=C:\Users\runneradmin\AppData\Roaming\go\env
2026-10-21T04:09:50.7030787Z set GOEXE=.exe
2026-10-21T04:09:50.7031075Z set GOEXPERIMENT=
2026-10-21T04:09:50.7031424Z set GOFIPS140=off
2026-10-21T04:09:50.7031716Z set GOFLAGS=
2026-10-21T04:09:50.7035643Z set GOGCCFLAGS=-m64 -mthreads -Wl,--no-gc-sections -fmessage-length=0 -ffile-prefix-map=C:\Users\RUNNER~1\AppData\Local\Temp\go-build1585242207=/tmp/go-build -gno-record-gcc-switches
2026-10-21T04:09:50.7037042Z set GOHOSTARCH=amd64
2026-10-21T04:09:50.7037368Z set GOHOSTOS=windows
2026-10-21T04:09:50.7037680Z set GOINSECURE=
2026-10-21T04:09:50.7038125Z set GOMOD=D:\a\AgentDock-Workbench\AgentDock-Workbench\go.mod
2026-10-21T04:09:50.7038717Z set GOMODCACHE=C:\Users\runneradmin\go\pkg\mod
2026-10-21T04:09:50.7039122Z set GONOPROXY=
2026-10-21T04:09:50.7039372Z set GONOSUMDB=
2026-10-21T04:09:50.7039644Z set GOOS=windows
2026-10-21T04:09:50.7039984Z set GOPATH=C:\Users\runneradmin\go
2026-10-21T04:09:50.7040396Z set GOPRIVATE=
2026-10-21T04:09:50.7040854Z set GOPROXY=https://proxy.golang.org,direct
2026-10-21T04:09:50.7041418Z set GOROOT=C:\hostedtoolcache\windows\go\1.26.5\x64
2026-10-21T04:09:50.7041961Z set GOSUMDB=sum.golang.org
2026-10-21T04:09:50.7042325Z set GOTELEMETRY=local
2026-10-21T04:09:50.7042866Z set GOTELEMETRYDIR=C:\Users\runneradmin\AppData\Roaming\go\telemetry
2026-10-21T04:09:50.7043466Z set GOTMPDIR=
2026-10-21T04:09:50.7043768Z set GOTOOLCHAIN=auto
2026-10-21T04:09:50.7044362Z set GOTOOLDIR=C:\hostedtoolcache\windows\go\1.26.5\x64\pkg\tool\windows_amd64
2026-10-21T04:09:50.7044965Z set GOVCS=
2026-10-21T04:09:50.7045225Z set GOVERSION=go1.26.5
2026-10-21T04:09:50.7045535Z set GOWORK=
2026-10-21T04:09:50.7045848Z set PKG_CONFIG=pkg-config
2026-10-21T04:09:50.7046084Z 
2026-10-21T04:09:50.7046526Z ##[endgroup]
2026-10-21T04:09:50.7346541Z ##[group]Run set -euo pipefail
2026-10-21T04:09:50.7346935Z set -euo pipefail
2026-10-21T04:09:50.7347172Z mkdir -p evidence
2026-10-21T04:09:50.7347469Z git rev-parse HEAD | tee evidence/source-sha.txt
2026-10-21T04:09:50.7348641Z go test -count=1 -timeout=15m ./internal/androidbridge ./internal/tool/androiddevice/... ./internal/tool/command/... ./internal/app ./internal/httpx ./internal/runtimeapi 2>&1 | tee evidence/core-tests.txt
2026-10-21T04:09:50.7371497Z shell: C:\Program Files\Git\bin\bash.EXE --noprofile --norc -e -o pipefail {0}
2026-10-21T04:09:50.7372124Z ##[endgroup]
2026-10-21T04:09:50.9889953Z b9925e4886bfbd465caffddf17ac5df4c962107e
2026-10-21T04:09:51.1641946Z go: downloading github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
2026-10-21T04:09:51.2305526Z go: downloading golang.org/x/sys v0.45.0
2026-10-21T04:09:51.3516507Z go: downloading github.com/go-oauth2/oauth2/v4 v4.5.4
2026-10-21T04:09:51.5841631Z go: downloading github.com/modelcontextprotocol/go-sdk v1.7.0
2026-10-21T04:09:51.5991384Z go: downloading github.com/UserExistsError/conpty v0.1.4
2026-10-21T04:09:51.6298116Z go: downloading github.com/uvwt/agentdock-protocol v0.8.2-0.20261004051622-257c54fb223b
2026-10-21T04:09:53.7286640Z go: downloading gopkg.in/yaml.v3 v3.0.1
2026-10-21T04:09:53.7287220Z go: downloading golang.org/x/text v0.14.0
2026-10-21T04:09:53.8369841Z go: downloading github.com/fsnotify/fsnotify v1.10.1
2026-10-21T04:09:54.0679848Z go: downloading github.com/chromedp/cdproto v0.0.0-20250724212937-08a3db8b4327
2026-10-21T04:09:54.3491573Z go: downloading github.com/chromedp/chromedp v0.14.2
2026-10-21T04:09:54.8466683Z go: downloading github.com/bmatcuk/doublestar/v4 v4.10.0
2026-10-21T04:09:54.8467443Z go: downloading github.com/rogpeppe/go-internal v1.15.0
2026-10-21T04:09:55.7736024Z go: downloading github.com/google/jsonschema-go v0.4.3
2026-10-21T04:09:56.9366179Z go: downloading github.com/yosida95/uritemplate/v3 v3.0.2
2026-10-21T04:09:57.0299530Z go: downloading golang.org/x/oauth2 v0.35.0
2026-10-21T04:09:57.0448550Z go: downloading golang.org/x/sync v0.20.0
2026-10-21T04:09:57.1381309Z go: downloading golang.org/x/time v0.15.0
2026-10-21T04:09:57.8608632Z go: downloading github.com/chromedp/sysutil v1.1.0
2026-10-21T04:09:57.8609513Z go: downloading github.com/go-json-experiment/json v0.0.0-20250725192818-e39067aee2d2
2026-10-21T04:09:57.8610902Z go: downloading github.com/gobwas/ws v1.4.0
2026-10-21T04:09:57.9986721Z go: downloading github.com/golang-jwt/jwt/v5 v5.3.1
2026-10-21T04:09:57.9987419Z go: downloading github.com/google/uuid v1.1.1
2026-10-21T04:09:58.0604025Z go: downloading github.com/segmentio/encoding v0.5.4
2026-10-21T04:09:58.1221923Z go: downloading github.com/gobwas/httphead v0.1.0
2026-10-21T04:09:58.1222737Z go: downloading github.com/gobwas/pool v0.2.1
2026-10-21T04:09:58.3247869Z go: downloading github.com/segmentio/asm v1.1.3
2026-10-21T04:10:15.2365766Z ok  	github.com/uvwt/agentdock/internal/androidbridge	0.042s
2026-10-21T04:10:17.7515318Z ok  	github.com/uvwt/agentdock/internal/tool/androiddevice	0.168s
2026-10-21T04:10:31.7290481Z --- FAIL: TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths (0.01s)
2026-10-21T04:10:31.7296306Z     command_runtime_service_windows_test.go:30: default WSL workdir = "/mnt/c/Users/runneradmin/AppData/Local/Temp/TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths1181005934/001", want bound workspace "/mnt/c/Users/RUNNER~1/AppData/Local/Temp/TestResolveWSLWorkdirAcceptsWindowsAndLinuxPaths1181005934/001"
2026-10-21T04:10:31.7298873Z --- FAIL: TestPrepareWSLSkillInvocationUsesPrivateDataDirectory (0.13s)
2026-10-21T04:10:31.7300060Z     --- FAIL: TestPrepareWSLSkillInvocationUsesPrivateDataDirectory/environment_only (0.04s)
2026-10-21T04:10:31.7307628Z         command_runtime_service_windows_test.go:162: command arguments = []string{"--distribution", "Ubuntu", "--cd", "/mnt/c/Users/runneradmin/AppData/Local/Temp/TestPrepareWSLSkillInvocationUsesPrivateDataDirectoryenvironmen3906946390/001", "--exec", "bash", "-lc", "pwd"}, want []string{"--distribution", "Ubuntu", "--cd", "/mnt/c/Users/RUNNER~1/AppData/Local/Temp/TestPrepareWSLSkillInvocationUsesPrivateDataDirectoryenvironmen3906946390/001", "--exec", "bash", "-lc", "pwd"}
2026-10-21T04:10:31.7311905Z FAIL
2026-10-21T04:10:31.7312734Z FAIL	github.com/uvwt/agentdock/internal/tool/command	13.925s
2026-10-21T04:10:31.7313563Z ok  	github.com/uvwt/agentdock/internal/tool/command/session	2.088s
2026-10-21T04:16:22.5507717Z ok  	github.com/uvwt/agentdock/internal/app	335.188s
2026-10-21T04:16:22.5508360Z ok  	github.com/uvwt/agentdock/internal/httpx	43.733s
2026-10-21T04:16:22.5510290Z ok  	github.com/uvwt/agentdock/internal/runtimeapi	0.782s
2026-10-21T04:16:22.5510934Z FAIL
2026-10-21T04:16:23.2251189Z ##[error]Process completed with exit code 1.
2026-10-21T04:16:23.2567014Z ##[group]Run actions/upload-artifact@v4
2026-10-21T04:16:23.2567438Z with:
2026-10-21T04:16:23.2567836Z   name: android-executor-core-Windows-b9925e4886bfbd465caffddf17ac5df4c962107e
2026-10-21T04:16:23.2568345Z   path: evidence
2026-10-21T04:16:23.2568600Z   retention-days: 14
2026-10-21T04:16:23.2568877Z   if-no-files-found: warn
2026-10-21T04:16:23.2569152Z   compression-level: 6
2026-10-21T04:16:23.2569411Z   overwrite: false
2026-10-21T04:16:23.2569661Z   include-hidden-files: false
2026-10-21T04:16:23.2569941Z ##[endgroup]
2026-10-21T04:16:23.4919172Z (node:7824) [DEP0040] DeprecationWarning: The `punycode` module is deprecated. Please use a userland alternative instead.
2026-10-21T04:16:23.4920684Z (Use `node --trace-deprecation ...` to show where the warning was created)
2026-10-21T04:16:23.4996970Z With the provided path, there will be 2 files uploaded
2026-10-21T04:16:23.5004613Z Artifact name is valid!
2026-10-21T04:16:23.5006109Z Root directory input is valid!
2026-10-21T04:16:23.7186176Z Beginning upload of artifact content to blob storage
2026-10-21T04:16:23.7443104Z (node:7824) [DEP0169] DeprecationWarning: `url.parse()` behavior is not standardized and prone to errors that have security implications. Use the WHATWG URL API instead. CVEs are not issued for `url.parse()` vulnerabilities.
2026-10-21T04:16:23.7700885Z Uploaded bytes 1238
2026-10-21T04:16:23.7888316Z Finished uploading artifact content to blob storage!
2026-10-21T04:16:23.7889653Z SHA256 digest of uploaded artifact zip is 62ceb6f25bee584809a63fd78881a9353ab123eb6b7057618b991d08c8267b96
2026-10-21T04:16:23.7892552Z Finalizing artifact upload
2026-10-21T04:16:24.0072993Z Artifact android-executor-core-Windows-b9925e4886bfbd465caffddf17ac5df4c962107e.zip successfully finalized. Artifact ID 12320277319
2026-10-21T04:16:24.0075029Z Artifact android-executor-core-Windows-b9925e4886bfbd465caffddf17ac5df4c962107e has been successfully uploaded! Final size is 1238 bytes. Artifact ID is 12320277319
2026-10-21T04:16:24.0085641Z Artifact download URL: https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/actions/runs/39542456758/artifacts/12320277319
2026-10-21T04:16:24.0457940Z Post job cleanup.
2026-10-21T04:16:24.2553271Z [command]"C:\Program Files\Git\bin\git.exe" version
2026-10-21T04:16:24.2934633Z git version 2.56.0.windows.2
2026-10-21T04:16:24.3008696Z Temporarily overriding HOME='D:\a\_temp\e6ea4386-b3fc-4f86-89e6-40c58e1d33e4' before making global git config changes
2026-10-21T04:16:24.3010143Z Adding repository directory to the temporary git global config as a safe directory
2026-10-21T04:16:24.3021077Z [command]"C:\Program Files\Git\bin\git.exe" config --global --add safe.directory D:\a\AgentDock-Workbench\AgentDock-Workbench
2026-10-21T04:16:24.3418701Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp core\.sshCommand
2026-10-21T04:16:24.3810811Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'core\.sshCommand' && git config --local --unset-all 'core.sshCommand' || :\""
2026-10-21T04:16:24.9661431Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp http\.https\:\/\/github\.com\/\.extraheader
2026-10-21T04:16:25.0053798Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "sh -c \"git config --local --name-only --get-regexp 'http\.https\:\/\/github\.com\/\.extraheader' && git config --local --unset-all 'http.https://github.com/.extraheader' || :\""
2026-10-21T04:16:25.5839121Z [command]"C:\Program Files\Git\bin\git.exe" config --local --name-only --get-regexp ^includeIf\.gitdir:
2026-10-21T04:16:25.6227430Z [command]"C:\Program Files\Git\bin\git.exe" submodule foreach --recursive "git config --local --show-origin --name-only --get-regexp remote.origin.url"
2026-10-21T04:16:26.2076186Z Cleaning up orphan processes
2026-10-21T04:16:26.2355322Z ##[warning]Node.js 20 is deprecated. The following actions target Node.js 20 but are being forced to run on Node.js 24: actions/setup-go@v5, actions/upload-artifact@v4. For more information see: https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/
```