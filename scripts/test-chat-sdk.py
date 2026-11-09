"""Unmodified OpenAI client smoke test; invoked by TestChatOpenAISDK.

Uses only the synthetic local provider and credentials from that Go test.
"""
import json
import sys

import openai

client = openai.OpenAI(base_url=sys.argv[1], api_key="chat-test")
model = "context-agent-1"
first = {"role": "user", "content": "My project is Iris."}
response = client.chat.completions.create(model=model, messages=[first])
assert response.id == "chatcmpl_fixture"
assert response.choices[0].message.content == " answer "
assert response.usage.total_tokens == 12
reply = client.chat.completions.create(
    model=model,
    messages=[first, response.choices[0].message, {"role": "user", "content": "What is its name?"}],
)
assert reply.choices[0].message.content == "Iris"
separate = client.chat.completions.create(model=model, messages=[{"role": "user", "content": "Separate chat"}])
assert separate.choices[0].message.content == "separate"

messages = [{"role": "user", "content": "Look up Berlin"}]
tools = [{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"], "additionalProperties": False}, "strict": True}}]
called = client.chat.completions.create(model=model, messages=messages, tools=tools, tool_choice="required", parallel_tool_calls=False)
assert called.choices[0].finish_reason == "tool_calls"
call = called.choices[0].message.tool_calls[0]
assert call.id == "call_sdk" and json.loads(call.function.arguments) == {"city": "Berlin"}
result = client.chat.completions.create(model=model, messages=[*messages, called.choices[0].message, {"role": "tool", "tool_call_id": call.id, "content": "found"}], tools=tools)
assert result.choices[0].message.content == "Berlin result"

chunks = list(client.chat.completions.create(model=model, messages=messages, stream=True, stream_options={"include_usage": True}))
assert all(chunk.id == "chatcmpl_fixture" for chunk in chunks)
assert "".join(chunk.choices[0].delta.content or "" for chunk in chunks if chunk.choices) == " streamed "
assert chunks[-2].choices[0].finish_reason == "length"
assert chunks[-1].choices == [] and chunks[-1].usage.total_tokens == 12
chunks = list(client.chat.completions.create(model=model, messages=messages, tools=tools, stream=True))
deltas = [tool for chunk in chunks for choice in chunk.choices for tool in choice.delta.tool_calls or []]
assert deltas[0].id == "call_sdk" and deltas[0].function.name == "lookup"
assert json.loads("".join(delta.function.arguments or "" for delta in deltas)) == {"city": "Berlin"}
assert chunks[-1].choices[0].finish_reason == "tool_calls"

structured = client.chat.completions.create(model=model, messages=messages, response_format={"type": "json_schema", "json_schema": {"name": "answer", "strict": True, "schema": {"type": "object", "properties": {"answer": {"type": "string"}}, "required": ["answer"], "additionalProperties": False}}})
assert json.loads(structured.choices[0].message.content) == {"answer": "structured"}
try:
    client.chat.completions.create(model=model, messages=messages, n=2)
except openai.BadRequestError as error:
    assert error.code == "unsupported_feature"
else:
    raise AssertionError("unsupported option accepted")
assert client.models.list().data[0].id == model
print(f"OpenAI SDK {openai.__version__}: supplied multi-turn, separate chats, tools, SSE, usage, structured output, errors, model discovery passed")
