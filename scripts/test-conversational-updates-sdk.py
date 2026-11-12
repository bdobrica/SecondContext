"""CL4 wiring with an unchanged OpenAI SDK and deterministic provider."""
import sys
import time
from openai import OpenAI

client = OpenAI(base_url=sys.argv[1], api_key="cl4-sdk-test")
model = "context-agent-1"
old = "Alex prefers short review requests."
correction = "I had that backwards: Alex prefers a detailed review brief, not a terse request."
action = "I sent Alex a request to review the API section of the launch proposal."
outcome = "Alex agreed, but only to review the API section of the launch proposal."


def await_context(present, absent=()):
    deadline = time.monotonic() + 25
    while True:
        answer = client.responses.create(model=model, input="Help me ask Alex for review.").output_text
        if all(item in answer for item in present) and all(item not in answer for item in absent):
            return answer
        assert time.monotonic() < deadline, "new working context did not become available"
        time.sleep(0.2)


client.chat.completions.create(model=model, messages=[{"role": "user", "content": old}])
await_context([old])
client.responses.create(model=model, input=correction)
await_context([correction, "correction:"], [old])
client.chat.completions.create(model=model, messages=[{"role": "user", "content": action}])
await_context([action])
client.responses.create(model=model, input=outcome)
await_context([outcome, "outcome_of:", correction], [old])
inspection = client.responses.create(model=model, input="What do you remember about Alex?").output_text
assert old in inspection and correction in inspection and "superseded" in inspection
fresh = client.chat.completions.create(model=model, messages=[{"role": "user", "content": "Help me ask Alex for review."}]).choices[0].message.content
assert outcome in fresh and correction in fresh and old not in fresh
print("Unchanged OpenAI client: CL4 natural correction, action/outcome capture, linked fresh-chat context and preserved source history passed (synthetic model).")
