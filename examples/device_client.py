#!/usr/bin/env python3
"""
r1rpc 设备端参考客户端（通用示例）。

它做的事：用 device key 登录拿到 WebSocket 地址 → 连上 → 上报本机支持的 action →
常驻接收任务、并发执行、回传结果，断线自动重连。

这个示例自带两个无需真机的 handler（ping / echo），所以连上 r1rpc 就能跑通整条链路。
要接真实能力（frida hook、算签名等），把 handler 换成你的实现即可。

依赖：
    pip install requests websocket-client

用法：
    python device_client.py \
        --server http://127.0.0.1:9876 \
        --device-key dk_xxxx \
        --group demo \
        --id device-01

生产部署（重要）：
    设备「在线」= 这个进程活着。进程一旦退出，面板里设备立刻离线。
    所以正式环境务必用进程管理器常驻 + 崩溃自动拉起，别用裸 `python ... &`。
    iOS 越狱(launchd) 的现成常驻模板见 examples/deploy/；其他环境机制类似。
    本脚本已支持被 SIGTERM/SIGINT 优雅停止（关闭 WS 后退出），配合进程管理器即可。
"""
import argparse
import json
import os
import signal
import threading
import time
from urllib.parse import quote

import requests
import websocket  # websocket-client


class R1RPCClient:
    def __init__(self, base_url, device_key, client_id, group, platform="python"):
        self.base_url = base_url.rstrip("/")
        self.device_key = device_key
        self.client_id = client_id
        self.group = group
        self.platform = platform
        self.handlers = {}
        self._running = False
        self._sock = None
        self._send_lock = threading.Lock()  # websocket-client.send() 非线程安全，必须串行化

    # 优雅停止：被 systemd/launchd 发 SIGTERM 时调用，唤醒阻塞的 recv() 并关闭 WS
    def stop(self):
        self._running = False
        sock = self._sock
        if sock:
            try:
                sock.abort()  # 强制唤醒正阻塞在 recv() 的接收循环，否则要等 socket 超时
            except Exception:
                pass
            try:
                sock.close()
            except Exception:
                pass

    # 注册一个 action 处理函数：handler(payload) -> 任意可 JSON 序列化的结果
    def register(self, action):
        def deco(fn):
            self.handlers[action] = fn
            return fn
        return deco

    def _login(self):
        resp = requests.post(
            f"{self.base_url}/api/client/login",
            json={
                "deviceKey": self.device_key,
                "clientId": self.client_id,
                "group": self.group,
                "platform": self.platform,
                "actions": list(self.handlers.keys()),  # 上报本机支持的 action
            },
            timeout=15,
        )
        resp.raise_for_status()
        body = resp.json()
        # 统一响应信封 {success, msg, data}
        if not body.get("success", True):
            raise RuntimeError(f"登录失败: {body.get('msg')}")
        data = body.get("data") or body
        token = data["token"]
        ws_base = self.base_url.replace("https://", "wss://").replace("http://", "ws://")
        return data.get("wsUrl") or f"{ws_base}/api/client/ws?token={quote(token)}"

    def _send(self, obj):
        # 多个 job 线程 + 心跳线程会并发发送，加锁防止帧流写乱被服务端断连
        if not self._sock:
            return
        with self._send_lock:
            self._sock.send(json.dumps(obj, ensure_ascii=False))

    def _heartbeat(self, sock):
        while self._running and self._sock is sock:
            try:
                self._send({"type": "heartbeat"})
            except Exception:
                return
            time.sleep(5)

    def _handle_job(self, job):
        action = job.get("action", "")
        started = time.time()
        handler = self.handlers.get(action)
        try:
            if handler is None:
                raise RuntimeError(f"未注册的 action: {action}")
            payload = handler(job.get("payload") or {})
            result = {"status": "success", "httpCode": 200, "payload": payload or {}, "error": ""}
        except Exception as e:  # 业务异常如实回传，不要吞
            result = {"status": "error", "httpCode": 500, "payload": {}, "error": str(e)}
        result["requestId"] = job.get("requestId", "")
        result["latencyMs"] = int((time.time() - started) * 1000)
        self._send({"type": "result", "result": result})

    def serve_forever(self):
        self._running = True
        while self._running:
            sock = None
            try:
                ws_url = self._login()
                print(f"[r1rpc] 登录成功，连接 {ws_url}")
                sock = websocket.create_connection(ws_url, timeout=30)
                self._sock = sock
                print("[r1rpc] WebSocket 已连接，等待任务…")
                threading.Thread(target=self._heartbeat, args=(sock,), daemon=True).start()
                while self._running and self._sock is sock:
                    raw = sock.recv()
                    if not raw:
                        break
                    msg = json.loads(raw)
                    if msg.get("type") == "job" and msg.get("job"):
                        # 每个任务一个线程，不阻塞接收循环（也别忘了发送已加锁）
                        threading.Thread(target=self._handle_job, args=(msg["job"],), daemon=True).start()
            except Exception as e:
                if not self._running:
                    break  # 收到停止信号，正常退出，不再重连
                print(f"[r1rpc] 连接断开: {e}，2s 后重连…")
                time.sleep(2)
            finally:
                self._sock = None
                if sock:
                    try:
                        sock.close()
                    except Exception:
                        pass


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--server", default=os.environ.get("R1RPC_SERVER", "http://127.0.0.1:9876"))
    p.add_argument("--device-key", default=os.environ.get("R1RPC_DEVICE_KEY", ""))
    p.add_argument("--group", default="demo")
    p.add_argument("--id", default="device-01")
    args = p.parse_args()

    if not args.device_key:
        raise SystemExit("缺少 device key：--device-key 或环境变量 R1RPC_DEVICE_KEY（在分组页复制）")

    client = R1RPCClient(args.server, args.device_key, args.id, args.group)

    @client.register("ping")
    def ping(_payload):
        return {"pong": True, "device": args.id, "ts": int(time.time())}

    @client.register("echo")
    def echo(payload):
        # 原样回显调用方传来的 payload
        return {"echo": payload}

    # 被 systemd/launchd（或 Ctrl-C）停止时，优雅关闭 WS 后退出
    def _shutdown(signum, _frame):
        print(f"[r1rpc] 收到信号 {signum}，正在退出…")
        client.stop()

    signal.signal(signal.SIGINT, _shutdown)
    signal.signal(signal.SIGTERM, _shutdown)

    print(f"[r1rpc] 启动：server={args.server} group={args.group} id={args.id} "
          f"actions={list(client.handlers.keys())}")
    client.serve_forever()


if __name__ == "__main__":
    main()
