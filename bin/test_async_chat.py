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
import base64
import json
import re
import sys
import time
from pathlib import Path
from typing import Any, Dict

import requests


MARKDOWN_DATA_IMAGE_PATTERN = re.compile(r"!\[[^\]]*\]\((data:image/[^)]+)\)")


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
        print_poll_payload(data)

        if status == "processing":
            time.sleep(poll_interval)
            continue

        if status == "error":
            return {"status": "error", "data": data}

        return {"status": "completed", "data": data}

    raise TimeoutError(f"poll timeout after {timeout}s")


def save_base64_image(image_base64: str, task_id: str) -> Path:
    """将 base64 图片保存到脚本同级目录。"""
    script_dir = Path(__file__).resolve().parent
    image_bytes = base64.b64decode(image_base64)
    output_path = script_dir / f"async_chat_{task_id}.png"
    output_path.write_bytes(image_bytes)
    return output_path


def extract_data_image_url(text: str) -> str | None:
    """从字符串中提取 data:image URL。"""
    if text.startswith("data:image"):
        return text

    match = MARKDOWN_DATA_IMAGE_PATTERN.search(text)
    if not match:
        return None
    return match.group(1)


def extract_image_base64(data: Dict[str, Any]) -> str | None:
    """从响应中提取图片 base64 内容。"""
    choices = data.get("choices") or []
    if not choices:
        return None

    message = choices[0].get("message") or {}
    content = message.get("content")

    if isinstance(content, str):
        data_image_url = extract_data_image_url(content)
        if data_image_url:
            _, _, encoded = data_image_url.partition(",")
            return encoded or None
        return None

    if not isinstance(content, list):
        return None

    for item in content:
        if not isinstance(item, dict):
            continue

        image_url = item.get("image_url")
        if isinstance(image_url, dict):
            url = image_url.get("url")
            if isinstance(url, str) and url.startswith("data:image"):
                _, _, encoded = url.partition(",")
                if encoded:
                    return encoded

        for key in ("b64_json", "image_base64"):
            value = item.get(key)
            if isinstance(value, str) and value:
                return value

    return None


def print_poll_payload(data: Dict[str, Any]) -> None:
    """按状态输出轮询响应，避免完整打印大块 base64。"""
    status = data.get("status")
    if status in ("processing", "error"):
        print(json.dumps(data, ensure_ascii=False, indent=2))
        return

    image_base64 = extract_image_base64(data)
    if image_base64:
        summary = {
            "id": data.get("id"),
            "object": data.get("object"),
            "created": data.get("created"),
            "image_base64_prefix": image_base64[:50],
        }
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        return

    print(json.dumps(data, ensure_ascii=False, indent=2))


def print_summary(result: Dict[str, Any]) -> None:
    """输出最终响应摘要。"""
    status = result["status"]
    data = result["data"]

    if status == "error":
        error_info = data.get("error") or {}
        print(f"[result] error: {error_info.get('message', 'unknown error')}")
        return

    image_base64 = extract_image_base64(data)
    if image_base64:
        task_id = str(data.get("id") or "unknown")
        output_path = save_base64_image(image_base64, task_id)
        print(f"[result] completed, image base64 prefix: {image_base64[:50]}")
        print(f"[result] image saved to: {output_path}")
        return

    choices = data.get("choices") or []
    if not choices:
        print("[result] completed, no choices field found")
        return

    message = choices[0].get("message") or {}
    content = message.get("content")
    print(f"[result] completed, first choice content: {content}")


def parse_args() -> argparse.Namespace:
    """解析命令行参数。"""
    parser = argparse.ArgumentParser(description="Test async chat completions API")
    parser.add_argument("--base-url", default="https://ai3.sleepinsum.com", help="API base URL, e.g. http://127.0.0.1:3000")
    parser.add_argument("--api-key", default="sk-PcldzS1wvLh0BicvsXI9cM28GhdmtcpqxgpeKHVwtSR7oOAc", help="Bearer token")
    parser.add_argument("--model", default="gemini-3.1-flash-image-preview", help="Model name")
    parser.add_argument("--prompt", default="一只小狗在打篮球", help="Prompt text")
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
