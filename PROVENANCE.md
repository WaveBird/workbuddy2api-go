# 这个仓库是怎么来的（基线可追溯）

## 一句话

本仓库 = **本机正在运行的 workbuddy2api 镜像对应的那份源码快照** + 一个补丁（出站转储）。
基线不是「某个看起来像的 fork」，可以用哈希逐项核对。

## 三项标识（三处必须一致）

| 位置 | 值 |
|---|---|
| 运行镜像 `ghcr.io/ithtelab/workbuddy2api:latest` 的 label `dev.workbuddy.upstream-id` | `caeb00aa6c752fa7286b78685a0c582d7233baee2e85286106ac29af1198d641` |
| 部署目录 `/Users/jame/workbuddy-deploy/workbuddy2api/.upstream-id` | 同上 |
| 本仓库 `.upstream-id` | 同上 |

镜像的其它元信息：`org.opencontainers.image.version = 2.11.10`，构建时间 `2026-11-04T05:51:02Z`。

复核方式：

```bash
# 镜像里的 upstream-id
docker inspect ghcr.io/ithtelab/workbuddy2api:latest \
  --format '{{index .Config.Labels "dev.workbuddy.upstream-id"}}'
# 仓库里的 upstream-id
cat .upstream-id
```

两者相同，才说明「改的就是正在跑的那一版」。

## 为什么不是 GitHub 上的 fork

上游 Go 版**没有可 fork 的仓库**（实测 2026-11-08）：

- `github.com/Sliverkiss/workbuddy2api` —— 404（作者已删除，`UPSTREAM-SRC.txt` 里也这么写）；
- `github.com/ithtelab/workbuddy2api` —— 404（只有 ghcr 镜像包，没有源码仓库）；
- `github.com/hawklithm/workbuddy2api` —— 存在，但那是**另一个同名实现（Python）**，与本项目无关。

所以源码以 MIT 快照随 WorkBuddy Manager 的 Release（`upstream-src`）分发；本仓库
就建立在这份快照上。若维护者将来建了上游源码仓库，把本仓库改成它的 fork 即可，
`upstream-id` 仍可用于核对基线。

## 相对基线的差异（全部在此）

```
$ diff -rq <本仓库> <部署目录>   # 排除 .git / auths / data / config.json
Files README.md differ                     # 顶部加一行「本 fork 的差异」
Files cmd/server/config.go differ          # 新增 outbound_trace 配置段
Files cmd/server/main.go differ            # 按配置装配转储器
Files config.example.json differ           # 增加示例段（默认 false）
Files internal/server/handler.go differ    # 入站 X-Trace-ID 透传到 ChatMeta
Files internal/upstream/client.go differ   # 三个出口接上转储
Only in internal/upstream: outbound_trace.go   # 新增：转储实现
Only in .: OUTBOUND-TRACE.md               # 新增：设计与开关说明
Only in <部署目录>: .github                # 本仓库未收录上游 CI（推送时 workflow 文件被拒）
```

## 构建与验证

```bash
# 构建（Go 1.22）
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22 sh -c 'go build ./...'
# 测试
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22 sh -c 'go test ./internal/upstream/ ./internal/server/'
```

本仓库提交时两者均通过。

## 许可

MIT，版权归原作者（`LICENSE`，© 2026 Sliverkiss）。本仓库仅在其之上增加出站转储能力，
再分发请保留 `LICENSE`、`UPSTREAM-SRC.txt` 与本文件。
