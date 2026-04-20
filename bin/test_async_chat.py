#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
异步 chat completion 联调脚本。

功能：
1. 调用 /v1/chat/completions/async 发起任务
2. 提取 uuid
3. 轮询 /v1/chat/completions/async/:uuid
4. 输出 processing / error / completed 状态
"""

import argparse
import json
import sys
import time
from typing import Any, Dict

import requests


def build_headers(api_key: str) -> Dict[str, str]:
    """构造统一请求头。"""
    return {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }


def submit_task(base_url: str, api_key: str, model: str, prompt: str) -> str:
    """提交异步 chat completion 任务并返回任务 UUID。"""
    url = f"{base_url.rstrip('/')}/v1/chat/completions/async"
    payload = {
        "model": model,
        "messages": [
            {
                "role": "user",
                "content": prompt,
            }
        ],
    }
    response = requests.post(url, headers=build_headers(api_key), json=payload, timeout=60)
    print(f"[submit] status={response.status_code}")
    print(response.text)
    response.raise_for_status()
    data = response.json()
    task_id = data.get("id")
    if not task_id:
        raise RuntimeError("submit response missing task id")
    return task_id


def poll_task(base_url: str, api_key: str, task_id: str, timeout: int, poll_interval: float) -> Dict[str, Any]:
    """轮询异步任务直到完成、失败或超时。"""
    url = f"{base_url.rstrip('/')}/v1/chat/completions/async/{task_id}"
    deadline = time.time() + timeout

    while time.time() < deadline:
        response = requests.get(url, headers=build_headers(api_key), timeout=60)
        print(f"[poll] status={response.status_code}")
        response.raise_for_status()

        data = response.json()
        status = data.get("status")
        print(json.dumps(data, ensure_ascii=False, indent=2))

        if status == "processing":
            time.sleep(poll_interval)
            continue

        if status == "error":
            return {"status": "error", "data": data}

        return {"status": "completed", "data": data}

    raise TimeoutError(f"poll timeout after {timeout}s")


def print_summary(result: Dict[str, Any]) -> None:
    """输出最终响应摘要。"""
    status = result["status"]
    data = result["data"]

    if status == "error":
        error_info = data.get("error") or {}
        print(f"[result] error: {error_info.get('message', 'unknown error')}")
        return

    choices = data.get("choices") or []
    if choices:
        message = choices[0].get("message") or {}
        content = message.get("content")
        print(f"[result] completed, first choice content: {content}")
    else:
        print("[result] completed, no choices field found")


def parse_args() -> argparse.Namespace:
    """解析命令行参数。"""
    parser = argparse.ArgumentParser(description="Test async chat completions API")
    parser.add_argument("--base-url", required=True, help="API base URL, e.g. http://127.0.0.1:3000")
    parser.add_argument("--api-key", required=True, help="Bearer token")
    parser.add_argument("--model", required=True, help="Model name")
    parser.add_argument("--prompt", required=True, help="Prompt text")
    parser.add_argument("--poll-interval", type=float, default=3.0, help="Polling interval in seconds")
    parser.add_argument("--timeout", type=int, default=300, help="Polling timeout in seconds")
    return parser.parse_args()


def main() -> int:
    """脚本入口。"""
    args = parse_args()
    try:
        task_id = submit_task(args.base_url, args.api_key, args.model, args.prompt)
        print(f"[submit] task_id={task_id}")
        result = poll_task(args.base_url, args.api_key, task_id, args.timeout, args.poll_interval)
        print_summary(result)
        return 0 if result["status"] == "completed" else 1
    except Exception as exc:  # noqa: BLE001
        print(f"[fatal] {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
