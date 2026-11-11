"use strict";
(() => {
  let token = "", subject = "", enabled = false, generation = 0, offset = 0, query = "", refreshSerial = 0;
  const $ = id => document.getElementById(id);
  const text = (tag, value, className) => {
    const node = document.createElement(tag);
    node.textContent = value;
    if (className) node.className = className;
    return node;
  };
  async function api(path, method = "GET", body) {
    const headers = {Authorization: `Bearer ${token}`};
    if (subject) headers["X-SecondContext-Subject"] = subject;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "omit", cache: "no-store", redirect: "error"});
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message || `Request failed (${response.status})`);
    return result;
  }
  async function action(fn, notice = "") {
    const current = generation;
    $("error").textContent = "";
    try { await fn(); if (current === generation) $("notice").textContent = notice; }
    catch (error) { if (current === generation) $("error").textContent = error.message; }
  }
  function button(label, fn) {
    const node = text("button", label);
    node.type = "button";
    node.onclick = async () => { node.disabled = true; await action(fn); node.disabled = false; };
    return node;
  }
  async function refresh() {
    const current = generation, serial = ++refreshSerial;
    const [settings, list] = await Promise.all([api("/memory/learning"), api(`/memory/observations?q=${encodeURIComponent(query)}&offset=${offset}`)]);
    if (current !== generation || serial !== refreshSerial) return;
    enabled = settings.enabled;
    $("learning-state").textContent = enabled ? "Learning enabled" : "Learning paused / off";
    $("toggle").textContent = enabled ? "Pause learning" : "Enable learning";
    $("memories").replaceChildren();
    const observations = list.data;
    $("previous").disabled = offset === 0; $("next").disabled = !list.has_more;
    $("page").textContent = `Observations ${observations.length ? offset + 1 : 0}–${offset + observations.length}${list.has_more ? " · More available" : ""}`;
    if (!observations.length) $("memories").append(text("p", "No matching conversational observations."));
    for (const memory of observations) {
      const meta = memory.metadata || {};
      const card = text("article", "", "card");
      card.append(text("h3", memory.summary));
      card.append(text("p", `Memory ID: ${memory.id} · Recall: ${memory.recall_status || "unspecified"}\nSource: ${meta.source_reference || memory.source}\nSpeaker: ${meta.speaker || "unspecified"} · Evidence: ${meta.evidence || "unspecified"}\nFirst observed: ${meta.observed_at || memory.created_at}\nTime scope: ${meta.time_scope || "unspecified"} · Expires: ${memory.expires_at || "no stated expiry"}`, "detail"));
      const details = text("details", "");
      details.append(text("summary", "Original source"), text("pre", memory.raw_text));
      card.append(details);
      if (meta.corrected_at) card.append(text("p", `Corrected by you: ${meta.corrected_at}`, "detail"));
      if (memory.source === "conversation") {
        const label = text("label", "Correct this observation");
        const edit = document.createElement("textarea"); edit.value = memory.summary; edit.rows = 3;
        label.append(edit); card.append(label);
        card.append(button("Save correction", async () => { await api(`/memory/${memory.id}`, "PATCH", {summary: edit.value}); clearAnswer(); await refresh(); }));
      }
      card.append(button("Forget observation", async () => { await api(`/memory/${memory.id}`, "DELETE"); clearAnswer(); await refresh(); }));
      $("memories").append(card);
    }
    $("captures").replaceChildren();
    for (const capture of settings.captures) {
      const row = document.createElement("tr");
      for (const value of [capture.id, capture.observed_at, capture.status, capture.attempts]) row.append(text("td", String(value)));
      $("captures").append(row);
    }
    $("workspace").hidden = false;
  }
  function clearAnswer() {
    $("answer").replaceChildren(); $("documents").replaceChildren(); $("evidence").replaceChildren(); $("evidence-ids").value = "";
  }
  async function inspectEvidence(ids) {
    const current = generation;
    $("evidence").replaceChildren();
    if (!ids) return;
    const result = await api(`/memory/evidence?ids=${encodeURIComponent(ids)}`);
    if (current !== generation) return;
    if (!result.data.length) $("evidence").append(text("p", "No current evidence is available for these IDs."));
    for (const memory of result.data) {
      const card = text("article", "", "card"), meta = memory.metadata || {};
      card.append(text("h3", memory.summary), text("p", `${memory.id}\nFirst observed: ${meta.observed_at || memory.created_at}\nSource: ${meta.source_reference || memory.source}\nEvidence: ${meta.evidence || "unspecified"}`, "detail"), text("pre", memory.raw_text));
      $("evidence").append(card);
    }
  }
  $("inspect-evidence").onsubmit = event => { event.preventDefault(); action(() => inspectEvidence($("evidence-ids").value.trim())); };
  $("ask").onsubmit = event => {
    event.preventDefault(); const current = generation;
    $("send").disabled = true; clearAnswer();
    action(async () => {
      const result = await api("/v1/responses", "POST", {model: "context-agent-1", input: $("question").value, temporary_chat: $("temporary").checked});
      if (current !== generation) return;
      $("answer").append(text("h3", "Answer"), text("pre", result.output_text));
      const packet = result.metadata?.context_packet || {};
      const ids = (packet.memory_context || []).map(memory => memory.id).join(",");
      $("evidence-ids").value = ids;
      for (const [id, reason] of Object.entries(packet.evidence_reasons || {})) $("answer").append(text("p", `${id}: ${reason}`, "detail"));
      const documents = packet.knowledge_context || [];
      if (documents.length) $("documents").append(text("h3", "Reference excerpts supplied to this answer"));
      for (const document of documents) {
        const card = text("article", "", "card");
        card.append(text("h4", document.title || "Reference"), text("p", `Document: ${document.document_id} · Chunk: ${document.chunk_id}\n${document.uri || ""}\n${(document.heading_path || []).join(" / ")}`, "detail"), text("pre", document.text));
        $("documents").append(card);
      }
      await inspectEvidence(ids);
      if (current === generation) await refresh();
    }).finally(() => { $("send").disabled = false; });
  };
  $("search").onsubmit = event => { event.preventDefault(); query = $("query").value.trim(); offset = 0; action(refresh); };
  $("previous").onclick = () => { offset = Math.max(0, offset - 50); action(refresh); };
  $("next").onclick = () => { offset += 50; action(refresh); };
  $("connect").onsubmit = event => {
    event.preventDefault(); generation++; offset = 0; query = ""; $("query").value = ""; $("question").value = ""; clearAnswer();
    token = $("token").value.trim(); subject = $("subject").value.trim(); $("token").value = "";
    $("workspace").hidden = true; $("evidence").replaceChildren(); $("evidence-ids").value = ""; $("memories").replaceChildren(); $("captures").replaceChildren();
    action(refresh, "Connected. Refresh to check background capture progress.");
  };
  $("toggle").onclick = () => action(async () => { await api("/memory/learning", "PUT", {enabled: !enabled}); await refresh(); });
  $("refresh").onclick = () => action(refresh);
  $("retry").onclick = () => action(async () => { await api("/memory/learning/retry", "POST", {}); await refresh(); });
  $("disconnect").onclick = () => {
    generation++; clearAnswer(); $("question").value = ""; $("query").value = ""; token = ""; subject = ""; $("subject").value = ""; $("token").value = "";
    $("workspace").hidden = true; $("evidence").replaceChildren(); $("evidence-ids").value = ""; $("memories").replaceChildren(); $("captures").replaceChildren();
    $("notice").textContent = "Disconnected."; $("error").textContent = "";
  };
})();
