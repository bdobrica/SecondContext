"use strict";
(() => {
  let token = "", subject = "", enabled = false, generation = 0;
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
    $("error").textContent = "";
    try { await fn(); $("notice").textContent = notice; }
    catch (error) { $("error").textContent = error.message; }
  }
  function button(label, fn) {
    const node = text("button", label);
    node.type = "button";
    node.onclick = async () => { node.disabled = true; await action(fn); node.disabled = false; };
    return node;
  }
  async function refresh() {
    const current = generation;
    const [settings, list] = await Promise.all([api("/memory/learning"), api("/memory?limit=100")]);
    if (current !== generation) return;
    enabled = settings.enabled;
    $("learning-state").textContent = enabled ? "Learning enabled" : "Learning paused / off";
    $("toggle").textContent = enabled ? "Pause learning" : "Enable learning";
    $("memories").replaceChildren();
    const observations = list.data.filter(memory => memory.source === "conversation");
    if (!observations.length) $("memories").append(text("p", "No conversational observations in the most recent 100 memories."));
    for (const memory of observations) {
      const meta = memory.metadata || {};
      const card = text("article", "", "card");
      card.append(text("h3", memory.summary));
      card.append(text("p", `Source: ${meta.source_reference || memory.source}\nSpeaker: ${meta.speaker || "unspecified"} · Evidence: ${meta.evidence || "unspecified"}\nFirst observed: ${meta.observed_at || memory.created_at}\nTime scope: ${meta.time_scope || "unspecified"} · Expires: ${memory.expires_at || "no stated expiry"}`, "detail"));
      const details = text("details", "");
      details.append(text("summary", "Original source"), text("pre", memory.raw_text));
      card.append(details);
      if (meta.corrected_at) card.append(text("p", `Corrected by you: ${meta.corrected_at}`, "detail"));
      if (memory.source === "conversation") {
        const label = text("label", "Correct this observation");
        const edit = document.createElement("textarea"); edit.value = memory.summary; edit.rows = 3;
        label.append(edit); card.append(label);
        card.append(button("Save correction", async () => { await api(`/memory/${memory.id}`, "PATCH", {summary: edit.value}); await refresh(); }));
      }
      card.append(button("Forget observation", async () => { await api(`/memory/${memory.id}`, "DELETE"); await refresh(); }));
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
  $("inspect-evidence").onsubmit = event => {
    event.preventDefault();
    const current = generation;
    action(async () => {
      const result = await api(`/memory/evidence?ids=${encodeURIComponent($("evidence-ids").value.trim())}`);
      if (current !== generation) return;
      $("evidence").replaceChildren();
      if (!result.data.length) $("evidence").append(text("p", "No current evidence is available for these IDs."));
      for (const memory of result.data) {
        const card = text("article", "", "card"), meta = memory.metadata || {};
        card.append(text("h3", memory.summary), text("p", `${memory.id}\nFirst observed: ${meta.observed_at || memory.created_at}\nSource: ${meta.source_reference || memory.source}\nEvidence: ${meta.evidence || "unspecified"}`, "detail"), text("pre", memory.raw_text));
        $("evidence").append(card);
      }
    });
  };
  $("connect").onsubmit = event => {
    event.preventDefault(); generation++;
    token = $("token").value.trim(); subject = $("subject").value.trim(); $("token").value = "";
    $("workspace").hidden = true; $("evidence").replaceChildren(); $("evidence-ids").value = ""; $("memories").replaceChildren(); $("captures").replaceChildren();
    action(refresh, "Connected. Refresh to check background capture progress.");
  };
  $("toggle").onclick = () => action(async () => { await api("/memory/learning", "PUT", {enabled: !enabled}); await refresh(); });
  $("refresh").onclick = () => action(refresh);
  $("retry").onclick = () => action(async () => { await api("/memory/learning/retry", "POST", {}); await refresh(); });
  $("disconnect").onclick = () => {
    generation++; token = ""; subject = ""; $("subject").value = ""; $("token").value = "";
    $("workspace").hidden = true; $("evidence").replaceChildren(); $("evidence-ids").value = ""; $("memories").replaceChildren(); $("captures").replaceChildren();
    $("notice").textContent = "Disconnected."; $("error").textContent = "";
  };
})();
