# 设备端常驻部署模板（iOS 越狱）

**设备「在线」= 客户端进程活着。** 进程一旦退出，面板里设备立刻离线。所以正式环境务必用
进程管理器常驻 + 崩溃自动拉起，别用裸 `python ... &`。

本目录给出 iOS 越狱设备的现成 launchd 模板 [`r1rpc-device-ios.plist`](r1rpc-device-ios.plist)
（占位符 `your-host` / `dk_xxxx` / `demo` / `device-01` 按需替换）。要点：`RunAtLoad`（开机自启）
+ `KeepAlive`（崩溃拉起），device key 走环境变量 `R1RPC_DEVICE_KEY`，**日志默认丢弃**
（客户端会打印每次调用结果，长跑会撑爆设备存储——见下）。

> 其他环境机制类似、按需自行套：macOS 用 launchd LaunchAgent、真 Linux 服务器用 systemd、
> Android（非 systemd）用 Magisk service.d / Termux。

## 安装

```bash
# rootless 越狱（Dopamine/ellekit 等），路径在 /var/jb 下
scp r1rpc-device-ios.plist root@设备IP:/var/jb/Library/LaunchDaemons/cc.example.r1rpc-device.plist
ssh root@设备IP 'launchctl load -w /var/jb/Library/LaunchDaemons/cc.example.r1rpc-device.plist'
ssh root@设备IP 'launchctl list | grep r1rpc'    # 看状态
```

### ⚠️ iOS 越狱的三个坑（踩过）

1. **frida 类客户端必须经登录 shell 启动。**
   launchd **直接 spawn** 的进程拿不到越狱的运行时注入上下文，frida 初始化会直接崩溃
   （表现：进程反复重启、日志只有 import 阶段输出）。解决：用 `zsh -lc 'cd ... && exec python3 ...'`
   包一层，python 作为登录 shell 的子进程才能继承注入上下文（从 SSH/shell fork 的进程没事，
   区别就在父级上下文）。模板里已经这么写了。
   > 纯 python（不向 App 注入）的客户端没有这个问题，可以直接写 python 路径。

2. **rootless 越狱需要 `POSIXSpawnType=Interactive` + `ExecuteAllowed=true`**（参考 frida-server 自己的 plist）。

3. **日志必须丢弃或轮转。** 客户端默认把每次调用结果打到 stdout，生产负载下几分钟就是几 MB，
   会撑爆设备存储。模板把 `StandardOutPath`/`StandardErrorPath` 都设成 `/dev/null`。

### 依赖

设备上需有 `python3` + `pip install requests websocket-client`；若客户端用 frida，还需
`frida` + `frida_tools`，并确保 `frida-server` 已常驻（它自己的 launchd plist 在同一目录）。
