"""EverMemOS client for the v1 open-source API.

Handles conversation metadata, message storage, and memory retrieval
against the in-cluster EverMemOS deployment.
"""

from __future__ import annotations

import logging
import os
import uuid
from datetime import datetime, timezone
from typing import Any

import requests

logger = logging.getLogger(__name__)

EVERMEMOS_URL = os.environ.get(
    "EVERMEMOS_URL",
    "http://evermemos.evermemos.svc.cluster.local:1995",
)
EVERMEMOS_TIMEOUT = int(os.environ.get("EVERMEMOS_TIMEOUT", "30"))
EVERMEMOS_SEARCH_TIMEOUT = int(os.environ.get("EVERMEMOS_SEARCH_TIMEOUT", "60"))

_HEADERS = {"Content-Type": "application/json"}


# -- Conversation metadata ---------------------------------------------------

def set_conversation_meta(
    group_id: str,
    user_id: str,
    assistant_id: str,
) -> dict[str, Any]:
    """Configure the assistant scene for a conversation group.

    Safe to call multiple times -- EverMemOS handles idempotency.
    """
    payload = {
        "scene": "assistant",
        "scene_desc": {"description": "Personal assistant conversation"},
        "name": "Personal Assistant",
        "group_id": group_id,
        "created_at": datetime.now(timezone.utc).isoformat(),
        "user_details": {
            user_id: {
                "full_name": user_id,
                "role": "user",
            },
            assistant_id: {
                "full_name": "Personal Assistant",
                "role": "assistant",
            },
        },
    }
    resp = requests.post(
        f"{EVERMEMOS_URL}/api/v1/memories/conversation-meta",
        json=payload,
        headers=_HEADERS,
        timeout=EVERMEMOS_TIMEOUT,
    )
    resp.raise_for_status()
    return resp.json()


# -- Message storage ---------------------------------------------------------

def store_message(
    group_id: str,
    sender: str,
    content: str,
    role: str = "user",
    sender_name: str | None = None,
) -> dict[str, Any]:
    """Store a single message for memory extraction.

    EverMemOS processes messages asynchronously -- it auto-detects
    conversation boundaries and extracts memories in the background.
    """
    payload = {
        "group_id": group_id,
        "message_id": str(uuid.uuid4()),
        "create_time": datetime.now(timezone.utc).isoformat(),
        "sender": sender,
        "sender_name": sender_name or sender,
        "role": role,
        "content": content,
    }
    resp = requests.post(
        f"{EVERMEMOS_URL}/api/v1/memories",
        json=payload,
        headers=_HEADERS,
        timeout=EVERMEMOS_TIMEOUT,
    )
    resp.raise_for_status()
    return resp.json()


# -- Retrieval ---------------------------------------------------------------

def search_memories(
    query: str,
    user_id: str | None = None,
    group_id: str | None = None,
    retrieve_method: str = "hybrid",
    top_k: int = 5,
    memory_types: list[str] | None = None,
) -> list[dict[str, Any]]:
    """Search for relevant memories.  Returns a flat list of memory dicts."""
    payload: dict[str, Any] = {
        "query": query,
        "retrieve_method": retrieve_method,
        "top_k": top_k,
    }
    if memory_types:
        payload["memory_types"] = memory_types
    if user_id:
        payload["user_id"] = user_id
    if group_id:
        payload["group_id"] = group_id

    resp = requests.get(
        f"{EVERMEMOS_URL}/api/v1/memories/search",
        json=payload,
        headers=_HEADERS,
        timeout=EVERMEMOS_SEARCH_TIMEOUT,
    )
    resp.raise_for_status()
    data = resp.json().get("data", {})
    return data.get("memories", [])


def fetch_profile(user_id: str, page_size: int = 10) -> list[dict[str, Any]]:
    """Fetch stable profile memories for a user."""
    payload = {
        "user_id": user_id,
        "memory_type": "profile",
        "page": 1,
        "page_size": page_size,
    }
    resp = requests.get(
        f"{EVERMEMOS_URL}/api/v1/memories",
        json=payload,
        headers=_HEADERS,
        timeout=EVERMEMOS_TIMEOUT,
    )
    resp.raise_for_status()
    data = resp.json().get("data", {})
    return data.get("memories", [])


def retrieve_context(query: str, user_id: str) -> str:
    """Retrieve relevant memories and format as a prompt block.

    Two-pronged approach:
      1. Profile (fetch) -- stable user facts
      2. Episodic + foresight (search) -- relevant past interactions
    """
    all_memories: list[dict[str, Any]] = []

    try:
        all_memories.extend(fetch_profile(user_id))
    except Exception as exc:
        logger.warning("Profile fetch failed: %s", exc)

    try:
        all_memories.extend(
            search_memories(
                query=query,
                user_id=user_id,
                retrieve_method="hybrid",
                top_k=5,
                memory_types=["episodic_memory", "foresight"],
            )
        )
    except Exception as exc:
        logger.warning("Memory search failed: %s", exc)

    if not all_memories:
        return "You don't have any prior memories about this user yet."

    lines = [
        "Here is what you remember about this user from previous conversations:"
    ]
    for mem in all_memories:
        mem_type = mem.get("memory_type", "unknown")
        content = mem.get("memory_content", mem.get("content", ""))
        if content:
            lines.append(f"- [{mem_type}] {content}")
    return "\n".join(lines)
