"""CL2 protocol acceptance with an unchanged OpenAI client and synthetic provider.

Only endpoint, credential and model setup are needed by the chat client. Learning
is enabled by the Go harness before this script starts. This checks context wiring,
not real-model extraction or advice quality.
"""
import sys
import time

from openai import OpenAI

client = OpenAI(base_url=sys.argv[1], api_key="natural-sdk-test")
model = "context-agent-1"
observation = "Alex prefers small, tightly scoped review requests."
client.chat.completions.create(model=model, messages=[{"role": "user", "content": observation}])
question = {"role": "user", "content": "Help me ask Alex for review."}
# Capture is asynchronous. A normal fresh-chat retry can see it once ready.
deadline = time.monotonic() + 20
while True:
    first = client.chat.completions.create(model=model, messages=[question])
    if observation in first.choices[0].message.content:
        break
    assert time.monotonic() < deadline, "background observation did not become available"
    time.sleep(0.1)

for endpoint in ("chat", "responses"):
    def ask(messages):
        if endpoint == "chat":
            return client.chat.completions.create(model=model, messages=messages).choices[0].message.content
        return client.responses.create(model=model, input=messages).output_text

    fresh = ask([question])
    assert observation in fresh
    assert "Draft the requested message" in fresh
    dialogue = [question, {"role": "assistant", "content": "Draft: Alex, could you review the API section?"}]
    assert observation in ask([*dialogue, {"role": "user", "content": "Make it warmer."}])
    alternatives = ask([*dialogue, {"role": "user", "content": "What are two other approaches?"}])
    assert observation in alternatives and "Offer a few useful alternative approaches" in alternatives
    assert observation not in ask([*dialogue, {"role": "user", "content": "How do I bake sourdough?"}])

chunks = list(client.chat.completions.create(model=model, messages=[question], stream=True))
assert "".join(choice.delta.content or "" for chunk in chunks for choice in chunk.choices) == "answer"
print("Unchanged OpenAI client: background capture, fresh Chat/Responses recall, drafting, alternatives, topic switch and Chat SSE passed (synthetic model).")
