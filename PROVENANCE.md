# 这个仓库是怎么来的（基线可追溯）

## 一句话

本仓库 = **本机正在运行的 workbuddy2api 镜像对应的那份源码快照** + 一个补丁（出站转储）。
基线不是「某个看起来像的 fork」，可以用哈希逐项核对。

## 三项标识（三处必须一致）

| 位置 | 值 |
|---|---|
| 运行镜像 `ghcr.io/ithtelab/workbuddy2api:latest` 的 label `dev.workbuddy.upstream-id` | `f0d14f4cb9d3bedd9ad6b58f94ab16c640abad5b1fd3a79f2952f9ec5bde4b95` |
| 部署目录 `/Users/jame/workbuddy-deploy/workbuddy2api/.upstream-id` | 同上 |
| 本仓库 `.upstream-id` | 同上 |

镜像的其它元信息：`org.opencontainers.image.version = 2.11.12`。

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

基线 = `upstream-id = f0d14f4c…bde4b95`（v2.11.12）的源码快照，随上游的 Release 包
（`workbuddy-manager` 的 `upstream-src` 资产）分发。

```
$ diff -rq <本仓库> <新版上游快照>   # 排除 .git
Files README.md differ                  # 顶部加「本 fork 的差异」+ 配套管理端链接
Files .upstream-id differ              # 本仓库的标识（值与基线一致）
Only in .: OUTBOUND-TRACE.md           # 新增：设计与开关说明
Only in .: PROVENANCE.md               # 新增：本文件（基线与来源）
```

补丁本身（出站转储）相对基线改了这些：

```
Files cmd/server/config.go differ          # 新增 outbound_trace 配置段
Files cmd/server/main.go differ            # 按配置装配转储器
Files config.example.json differ           # 增加示例段（默认 false）
Files internal/server/handler.go differ    # 入站 X-Trace-ID 透传到 ChatMeta
Files internal/upstream/client.go differ   # 三个出口接上转储
Only in internal/upstream: outbound_trace.go   # 新增：转储实现
```

> `internal/upstream/model.json` 另有一处差异：上游发布包里的那份是 27 条模型，
> 而**部署目录**（上游维护者实际在跑的那份）是 38 条 —— 多出来的
> `hy4` / `hy4free` / `hy4std` / `minimax-m2.7` / `minimax-m3.1-flash` /
> `kimi-k3-1` / `kimi-k3.1-pro` / `step-5-preview` / `mimo-v2.6-pro` /
> `mimo-v2.6-flash` / `deepseek-v4-flash-vision-exp` 是维护者后补的。
> 本仓库取**部署目录**那份（38 条），否则重建镜像会把这些模型回滚掉。

## 构建与验证

```bash
# 构建（Go 1.22）
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22 sh -c 'go build ./...'
# 测试
docker run --rm -v "$PWD":/src -w /src -e GOFLAGS=-mod=mod golang:1.22 sh -c 'go test ./internal/upstream/ ./internal/server/ ./cmd/server/'
```

本仓库提交时三者均通过。

## 许可

MIT，版权归原作者（`LICENSE`，© 2026 Sliverkiss）。本仓库仅在其之上增加出站转储能力，
再分发请保留 `LICENSE`、`UPSTREAM-SRC.txt` 与本文件。
