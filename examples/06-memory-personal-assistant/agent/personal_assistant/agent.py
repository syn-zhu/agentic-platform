"""BYO ADK agent: personal assistant with long-term memory via EverMemOS.

Memory integration happens transparently through ADK callbacks:
  - before_model_callback: stores the user message, retrieves relevant
    memories, and injects them into the system instruction.
  - after_model_callback: stores the assistant's response.

The agent itself sees an enriched system prompt with memory context --
no explicit memory tools are needed for the basic flow.
"""

from __future__ import annotations

import logging
import os

from google.adk.agents import Agent
from google.adk.models.lite_llm import LiteLlm
from google.genai import types

from . import memory

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Configuration (from environment)
# ---------------------------------------------------------------------------
USER_ID = os.environ.get("MEMORY_USER_ID", "user")
ASSISTANT_ID = os.environ.get("MEMORY_ASSISTANT_ID", "assistant")
GROUP_ID = os.environ.get("MEMORY_GROUP_ID", f"assistant_{USER_ID}")

# ---------------------------------------------------------------------------
# LLM — Claude via AgentGateway proxy
# ---------------------------------------------------------------------------
model = LiteLlm(
    model=os.environ.get("LLM_MODEL", "anthropic/claude-sonnet-4-20250514"),
    base_url=os.environ.get(
        "LLM_BASE_URL",
        "http://agentgateway-proxy.agentgateway-system.svc.cluster.local"
        "/llm/default/anthropic",
    ),
)

# ---------------------------------------------------------------------------
# Set up conversation metadata on import (idempotent)
# ---------------------------------------------------------------------------
try:
    memory.set_conversation_meta(GROUP_ID, USER_ID, ASSISTANT_ID)
    logger.info("Conversation meta set: group_id=%s", GROUP_ID)
except Exception as exc:
    logger.warning("Could not set conversation meta (may already exist): %s", exc)

# ---------------------------------------------------------------------------
# Callbacks — transparent memory integration
# ---------------------------------------------------------------------------

BASE_INSTRUCTION = """\
You are a helpful personal assistant with long-term memory.
You remember details about the user from previous conversations and use that
context to provide personalized, relevant responses.

{memory_context}

Guidelines:
- Reference memories naturally, don't list them mechanically
- If you remember something relevant, weave it into your response
- If you don't have relevant memories, just respond helpfully
- Be conversational and warm
"""


def before_model_callback(callback_context, llm_request):
    """Store the user message and inject memory context into the system prompt.

    Flow:
      1. Extract the latest user message from the LLM request
      2. Store it in EverMemOS for future memory extraction
      3. Retrieve relevant memories (profile + episodic search)
      4. Inject the memory context into the system instruction
    """
    # Extract latest user message
    user_message = None
    if llm_request.contents:
        for content in reversed(llm_request.contents):
            if content.role == "user" and content.parts:
                user_message = content.parts[0].text
                break

    if not user_message:
        return None  # proceed without modification

    # Store the user message (fire-and-forget -- don't block on extraction)
    try:
        memory.store_message(
            group_id=GROUP_ID,
            sender=USER_ID,
            content=user_message,
            role="user",
            sender_name=USER_ID,
        )
    except Exception as exc:
        logger.warning("Failed to store user message: %s", exc)

    # Retrieve memory context
    memory_context = memory.retrieve_context(query=user_message, user_id=USER_ID)

    # Inject into system instruction
    enriched_instruction = BASE_INSTRUCTION.format(memory_context=memory_context)
    llm_request.config.system_instruction = types.Content(
        role="system",
        parts=[types.Part(text=enriched_instruction)],
    )

    return None  # proceed with the (modified) request


def after_model_callback(callback_context, llm_response):
    """Store the assistant's response in EverMemOS for future memory extraction."""
    if not llm_response or not llm_response.content:
        return llm_response

    # Extract text from the response
    assistant_text = ""
    if llm_response.content.parts:
        text_parts = [p.text for p in llm_response.content.parts if p.text]
        assistant_text = "\n".join(text_parts)

    if assistant_text:
        try:
            memory.store_message(
                group_id=GROUP_ID,
                sender=ASSISTANT_ID,
                content=assistant_text,
                role="assistant",
                sender_name="Personal Assistant",
            )
        except Exception as exc:
            logger.warning("Failed to store assistant response: %s", exc)

    return llm_response


# ---------------------------------------------------------------------------
# Root agent
# ---------------------------------------------------------------------------
root_agent = Agent(
    model=model,
    name="personal_assistant",
    description=(
        "A personal assistant that uses EverMemOS for long-term memory. "
        "Remembers user preferences, past conversations, and context across sessions."
    ),
    instruction=BASE_INSTRUCTION.format(
        memory_context="Memory context will be injected dynamically before each response."
    ),
    before_model_callback=before_model_callback,
    after_model_callback=after_model_callback,
)
