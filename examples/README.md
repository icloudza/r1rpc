# examples

两个最小可跑示例，串起 r1rpc 的两端。

```
pip install requests websocket-client
```

## device_client.py —— 设备端

用 device key 登录、注册 action、常驻收发、断线重连的参考实现。自带 `ping` / `echo` 两个无需真机的 handler，连上就能跑通整条链路；接真实能力把 handler 换成你的实现即可。

```bash
python device_client.py --server http://127.0.0.1:9876 \
    --device-key dk_xxxx --group demo --id device-01
```

> device key 在面板「分组」页复制。跑起来后该设备会在「设备」页显示在线，分组的可用 action 自动出现 `ping` / `echo`。

要点：每个任务起独立线程执行，但所有 WebSocket 发送都过同一把锁——`websocket-client` 的 `send()` 非线程安全，并发写会把帧流写乱导致服务端断连。

> **常驻部署**：设备「在线」= 客户端进程活着，正式环境务必用进程管理器常驻 + 崩溃自动拉起。
> iOS 越狱(launchd) 的现成常驻模板见 [`deploy/`](deploy/)——
> iOS 越狱有几个非踩不可的坑（frida 需 `zsh -lc` 包装拿注入上下文、日志要丢弃防撑爆存储），都写清了；
> 其他环境（macOS launchd / Linux systemd / Android Magisk）机制类似，按需自套。

## caller.py —— 调用方

从后端发起一次调用：

```bash
# 免鉴权分组
python caller.py --group demo --action ping

# apikey 分组（带分组的调用 API Key）
python caller.py --group demo --action echo --api-key ak_xxxx --payload '{"hello":"world"}'
```

等价 curl：

```bash
curl -X POST "http://127.0.0.1:9876/rpc/demo/ping" \
  -H "X-API-Key: ak_xxxx" \
  -H "Content-Type: application/json" \
  -d '{"payload":{},"timeoutSeconds":15}'
```
