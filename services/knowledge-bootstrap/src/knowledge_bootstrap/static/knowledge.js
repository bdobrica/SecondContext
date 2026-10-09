"use strict";

// A credential-free Jinja shell with a small API client. No cookies or browser storage.
const $ = (id) => document.getElementById(id);
const PAGE = 50, DETAIL_PAGE = 20;
const state = {token: "", epoch: 0, controllers: new Set(), offset: 0, sources: [], selected: null,
  detailEpoch: 0, docOffset: 0, document: null, chunkOffset: 0, sourceRequest: 0, docCount: 0, chunkCount: 0};
const active = (source) => !["ready", "failed"].includes(source.status);
const date = (value) => value ? new Date(value).toLocaleString() : "—";
const node = (tag, text, className) => {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
};
function message(id, text, error = false) {
  const element = $(id);
  element.textContent = text;
  element.hidden = !text;
  element.className = error ? "error" : "success";
}
function errorText(data, status) {
  const error = data?.error;
  const fields = (error?.fields || []).map((field) => `${field.location.join(".")}: ${field.message}`);
  return [error?.code || `http_${status}`, error?.detail || "Request failed", ...fields].join(" · ");
}
function disconnected() {
  state.token = "";
  state.epoch++;
  state.detailEpoch++;
  for (const controller of state.controllers) controller.abort();
  state.controllers.clear();
  state.sources = []; state.selected = null; state.document = null; state.offset = 0;
  $("workspace").hidden = true; $("login").hidden = false; $("disconnect").hidden = true;
  $("connection-status").textContent = "Disconnected";
  $("token").value = "";
  for (const id of ["source-list", "jobs", "documents", "chunks", "results"]) $(id).replaceChildren();
  $("source-filter").replaceChildren(node("option", "All sources"));
  $("source-filter").firstChild.value = "";
  $("source-detail").hidden = true; $("chunk-detail").hidden = true;
  for (const id of ["add-dialog", "delete-dialog"]) $(id).close();
  for (const id of ["website-form", "file-form", "paste-form", "search-form"]) $(id).reset();
  message("notice", ""); message("add-status", ""); message("delete-status", "");
  $("search-status").textContent = "Results will appear here.";
}
function checkEpoch(epoch) {
  if (epoch !== state.epoch) throw new DOMException("Session changed", "AbortError");
}
async function api(path, {method = "GET", body, key} = {}) {
  const epoch = state.epoch, controller = new AbortController();
  state.controllers.add(controller);
  const timeout = setTimeout(() => controller.abort(), 90000);
  try {
    const headers = {Authorization: `Bearer ${state.token}`};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (key) headers["Idempotency-Key"] = key;
    const response = await fetch(path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal, credentials: "omit", cache: "no-store"});
    checkEpoch(epoch);
    const data = response.status === 204 ? null : await response.json();
    checkEpoch(epoch);
    if (!response.ok) {
      if (response.status === 401) disconnected();
      throw new Error(errorText(data, response.status));
    }
    return data;
  } catch (error) {
    if (error.name === "AbortError" && epoch === state.epoch) throw new Error("Request timed out. Reload status before retrying.");
    throw error;
  } finally { clearTimeout(timeout); state.controllers.delete(controller); }
}
async function action(target, work, status = "notice") {
  const epoch = state.epoch;
  target.disabled = true;
  try { await work(); }
  catch (error) {
    if (error.name !== "AbortError" && (epoch === state.epoch || !state.token)) message(status, error.message, true);
  } finally { target.disabled = false; syncPager(); }
}
function syncPager() {
  $("sources-prev").disabled = state.offset === 0;
  $("sources-next").disabled = state.sources.length < PAGE;
  $("docs-prev").disabled = state.docOffset === 0;
  $("docs-next").disabled = state.docCount < DETAIL_PAGE;
  $("chunks-prev").disabled = state.chunkOffset === 0;
  $("chunks-next").disabled = state.chunkCount < DETAIL_PAGE;
}
function reference(parent, uri) {
  // Filenames/paste references remain text. Never turn arbitrary source URIs into active schemes.
  try {
    const url = new URL(uri);
    if (["https:", "http:"].includes(url.protocol) && !url.username && !url.password) {
      const link = node("a", uri); link.href = url.href; link.target = "_blank"; link.rel = "noopener noreferrer";
      parent.append(link); return;
    }
  } catch { /* Non-web source reference. */ }
  parent.append(node("span", uri || "—"));
}
function renderSources() {
  const list = $("source-list"); list.replaceChildren();
  for (const source of state.sources) {
    const row = node("tr"), name = node("td"), button = node("button", source.name, "source-name");
    button.addEventListener("click", () => action(button, () => selectSource(source.id)));
    name.append(button, node("div", `${source.kind} · ${source.format || "detecting format"}`, "muted small"));
    if (source.last_error_code) name.append(node("div", `${source.last_error_code}: ${source.last_error_detail}`, "error small"));
    const status = node("td"); status.append(node("span", source.status, `badge ${source.status}`));
    row.append(name, status, node("td", `${source.document_count} / ${source.chunk_count}`), node("td", date(source.last_ingested_at)));
    list.append(row);
  }
  if (!state.sources.length) {
    const cell = node("td", state.offset ? "No more sources on this page." : "No sources yet. Add your first reference.");
    cell.colSpan = 4; const row = node("tr"); row.append(cell); list.append(row);
  }
  syncPager();
  $("source-page").textContent = `Page ${state.offset / PAGE + 1}`;
  const filter = $("source-filter"), current = filter.value;
  const previous = Array.from(filter.options).find((option) => option.value === current);
  const all = node("option", "All sources"); all.value = ""; filter.replaceChildren(all);
  const available = new Map(state.sources.map((source) => [source.id, source.name]));
  if (state.selected) available.set(state.selected.id, state.selected.name);
  if (current && previous) available.set(current, previous.textContent);
  for (const [id, name] of available) { const option = node("option", name); option.value = id; filter.append(option); }
  filter.value = current;
}
async function loadSources() {
  const sequence = ++state.sourceRequest;
  const sources = await api(`/v1/sources?limit=${PAGE}&offset=${state.offset}`);
  if (sequence !== state.sourceRequest) return;
  state.sources = sources; renderSources();
}
function renderDetail(source) {
  $("source-detail").hidden = false; $("detail-title").textContent = source.name;
  $("detail-summary").textContent = `${source.kind} · ${source.format || "detecting format"} · ${source.status} · stage: ${source.latest_stage || "pending"} · ${source.document_count} documents / ${source.chunk_count} chunks · Last ingested: ${date(source.last_ingested_at)}`;
  $("detail-uri").replaceChildren(); reference($("detail-uri"), source.source_uri || `Source ${source.id}`);
  message("detail-error", source.last_error_code ? `${source.last_error_code}: ${source.last_error_detail}` : "", true);
}
async function loadDetail(sourceId, detailEpoch) {
  const [source, jobs] = await Promise.all([api(`/v1/sources/${sourceId}`), api(`/v1/sources/${sourceId}/jobs?limit=10`)]);
  if (detailEpoch !== state.detailEpoch) return;
  state.selected = source; renderDetail(source);
  $("jobs").replaceChildren();
  for (const job of jobs) {
    const item = node("div", undefined, "job");
    item.append(node("div", `${job.status} · ${job.stage} · ${job.documents_processed}/${job.documents_found} documents · ${job.chunks_created} chunks`),
      node("div", `Created ${date(job.created_at)} · Finished ${date(job.finished_at)}`, "muted small"));
    if (job.error_code) item.append(node("div", `${job.error_code}: ${job.error_detail}`, "error"));
    item.append(node("div", `Job ${job.id}`, "reference")); $("jobs").append(item);
  }
}
async function selectSource(id) {
  const detailEpoch = ++state.detailEpoch;
  state.selected = null; state.document = null; state.docOffset = 0; state.chunkOffset = 0;
  $("source-detail").hidden = true;
  $("chunk-detail").hidden = true; $("documents").replaceChildren(); $("jobs").replaceChildren();
  await loadDetail(id, detailEpoch);
  if (detailEpoch !== state.detailEpoch) return;
  await loadDocuments(); renderSources();
}
async function loadDocuments() {
  const sourceId = state.selected.id, detailEpoch = state.detailEpoch, offset = state.docOffset;
  const docs = await api(`/v1/sources/${sourceId}/documents?limit=${DETAIL_PAGE}&offset=${offset}`);
  if (detailEpoch !== state.detailEpoch || offset !== state.docOffset) return;
  $("documents").replaceChildren();
  for (const doc of docs) {
    const item = node("div", undefined, "document"), button = node("button", doc.title, "document-name");
    button.addEventListener("click", () => action(button, async () => {
      state.document = doc; state.chunkOffset = 0; await loadChunks();
    }));
    const uri = node("div", undefined, "reference"); reference(uri, doc.uri);
    item.append(button, node("span", ` · ${doc.format || "unknown"}`, "muted small"), uri); $("documents").append(item);
  }
  if (!docs.length) $("documents").append(node("p", "No documents on this page yet.", "muted"));
  state.docCount = docs.length; syncPager();
}
function chunkEvidence(chunk) {
  const item = node("div", undefined, "chunk");
  const location = chunk.heading_path?.join(" › ") || "No section heading";
  item.append(node("strong", location));
  if (chunk.page_start) item.append(node("div", `Pages ${chunk.page_start}–${chunk.page_end}`));
  item.append(node("p", `Chunk ${chunk.id} · #${chunk.ordinal} · ${chunk.token_count} tokens`, "reference"));
  const details = node("details"), text = node("pre", chunk.text);
  details.append(node("summary", "Full chunk text"), text); item.append(details);
  if (Object.keys(chunk.metadata_json || {}).length) {
    const provenance = node("details"); provenance.append(node("summary", "Provenance metadata"), node("pre", JSON.stringify(chunk.metadata_json, null, 2))); item.append(provenance);
  }
  return item;
}
async function loadChunks() {
  const doc = state.document, detailEpoch = state.detailEpoch, offset = state.chunkOffset;
  const chunks = await api(`/v1/documents/${doc.id}/chunks?limit=${DETAIL_PAGE}&offset=${offset}`);
  if (detailEpoch !== state.detailEpoch || doc !== state.document || offset !== state.chunkOffset) return;
  $("chunk-detail").hidden = false; $("chunk-title").textContent = `Chunks · ${doc.title}`; $("chunks").replaceChildren();
  for (const chunk of chunks) $("chunks").append(chunkEvidence(chunk));
  if (!chunks.length) $("chunks").append(node("p", "No chunks on this page yet.", "muted"));
  state.chunkCount = chunks.length; syncPager();
}
$("login-form").addEventListener("submit", (event) => {
  event.preventDefault(); const token = $("token").value.trim();
  action(event.submitter, async () => {
    disconnected(); state.token = token;
    try { await loadSources(); }
    catch (error) { disconnected(); throw error; }
    $("token").value = ""; $("login").hidden = true; $("workspace").hidden = false; $("disconnect").hidden = false;
    $("connection-status").textContent = "Connected"; message("notice", "");
  });
});
$("disconnect").addEventListener("click", disconnected);
$("reload-sources").addEventListener("click", (event) => action(event.target, async () => {
  await loadSources(); if (state.selected) { await loadDetail(state.selected.id, state.detailEpoch); await loadDocuments(); }
  message("notice", "Sources updated.");
}));
for (const [id, delta] of [["sources-prev", -PAGE], ["sources-next", PAGE]]) {
  $(id).addEventListener("click", (event) => action(event.target, async () => { state.offset += delta; await loadSources(); }));
}
$("close-detail").addEventListener("click", () => { state.detailEpoch++; state.selected = null; state.document = null; $("source-detail").hidden = true; });
for (const [id, delta] of [["docs-prev", -DETAIL_PAGE], ["docs-next", DETAIL_PAGE]]) {
  $(id).addEventListener("click", (event) => action(event.target, async () => { state.docOffset += delta; await loadDocuments(); }));
}
for (const [id, delta] of [["chunks-prev", -DETAIL_PAGE], ["chunks-next", DETAIL_PAGE]]) {
  $(id).addEventListener("click", (event) => action(event.target, async () => { state.chunkOffset += delta; await loadChunks(); }));
}
for (const [id, route] of [["refresh-source", "refresh"], ["reindex-source", "reindex"]]) {
  $(id).addEventListener("click", (event) => action(event.target, async () => {
    const sourceId = state.selected.id;
    await api(`/v1/sources/${sourceId}/${route}`, {method: "POST"});
    await selectSource(sourceId); await loadSources(); message("notice", `${route === "refresh" ? "Refresh" : "Reindex"} queued.`);
  }));
}
$("add-source").addEventListener("click", () => { message("add-status", ""); $("add-dialog").showModal(); });
$("close-add").addEventListener("click", () => $("add-dialog").close());
$("add-mode").addEventListener("change", () => {
  for (const mode of ["website", "file", "paste"]) $(`${mode}-form`).hidden = mode !== $("add-mode").value;
  message("add-status", "");
});
$("crawl-scope").addEventListener("change", () => {
  $("max-pages").value = $("crawl-scope").value === "page" ? 1 : Math.min(10, Number(document.querySelector("main").dataset.maxPages));
});
async function accepted(data) {
  $("add-dialog").close();
  for (const mode of ["website", "file", "paste"]) $(`${mode}-form`).reset();
  state.offset = 0; await loadSources(); await selectSource(data.source.id);
  message("notice", "Source accepted. Ingestion status will update automatically.");
}
function requestKey() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) => value.toString(16).padStart(2, "0")).join("");
}
for (const mode of ["website", "paste"]) {
  $(`${mode}-form`).addEventListener("submit", (event) => {
    event.preventDefault(); action(event.submitter, async () => {
      message("add-status", "Submitting…");
      let payload;
      if (mode === "website") payload = {kind: "url", name: $("website-name").value.trim() || $("website-url").value,
        source_uri: $("website-url").value, config_json: {scope: $("crawl-scope").value, max_pages: Number($("max-pages").value)}};
      else {
        if (new TextEncoder().encode($("paste-text").value).length > Number(document.querySelector("main").dataset.maxTextBytes)) throw new Error("input_too_large · Text exceeds the UTF-8 byte limit.");
        payload = {kind: "text", name: $("paste-name").value.trim() || "Pasted text", text: $("paste-text").value, format: $("paste-format").value};
      }
      await accepted(await api("/v1/sources", {method: "POST", body: payload, key: requestKey()}));
    }, "add-status");
  });
}
function upload(data) {
  const epoch = state.epoch, xhr = new XMLHttpRequest();
  const controller = {abort: () => xhr.abort()}; state.controllers.add(controller);
  return new Promise((resolve, reject) => {
    xhr.open("POST", "/v1/sources/upload"); xhr.timeout = 90000;
    xhr.setRequestHeader("Authorization", `Bearer ${state.token}`); xhr.setRequestHeader("Idempotency-Key", requestKey());
    xhr.upload.addEventListener("progress", (event) => {
      if (epoch !== state.epoch) return;
      $("upload-progress").hidden = false;
      if (event.lengthComputable) $("upload-progress").value = event.loaded * 100 / event.total;
      else $("upload-progress").removeAttribute("value");
      message("add-status", event.loaded === event.total ? "Upload complete. Creating ingestion job…" : "Uploading…");
    });
    xhr.onload = () => {
      try {
        checkEpoch(epoch); const body = JSON.parse(xhr.responseText);
        if (xhr.status < 200 || xhr.status >= 300) {
          if (xhr.status === 401) disconnected();
          throw new Error(errorText(body, xhr.status));
        }
        resolve(body);
      } catch (error) { reject(error); }
    };
    xhr.onerror = () => reject(new Error("Upload failed. Check the connection and reload source status."));
    xhr.ontimeout = () => reject(new Error("Upload timed out. Reload source status before retrying."));
    xhr.onabort = () => reject(new DOMException("Upload cancelled", "AbortError"));
    xhr.onloadend = () => state.controllers.delete(controller);
    xhr.send(data);
  });
}
$("file-form").addEventListener("submit", (event) => {
  event.preventDefault(); action(event.submitter, async () => {
    const file = $("file").files[0], limits = document.querySelector("main").dataset;
    if (file.size > Math.max(Number(limits.maxFileBytes), Number(limits.maxTextBytes))) throw new Error("input_too_large · File exceeds the upload byte limit.");
    const body = new FormData(); body.append("file", file); body.append("name", $("file-name").value.trim() || file.name); body.append("format", $("file-format").value);
    $("upload-progress").value = 0; $("upload-progress").hidden = false; message("add-status", "Uploading…");
    try { await accepted(await upload(body)); } finally { $("upload-progress").hidden = true; }
  }, "add-status");
});
$("delete-source").addEventListener("click", () => {
  $("delete-description").textContent = `Delete “${state.selected.name}”?`;
  $("delete-dialog").dataset.sourceId = state.selected.id;
  message("delete-status", ""); $("delete-dialog").showModal();
});
$("cancel-delete").addEventListener("click", () => $("delete-dialog").close());
$("confirm-delete").addEventListener("click", (event) => action(event.target, async () => {
  const id = $("delete-dialog").dataset.sourceId; message("delete-status", "Deleting…");
  await api(`/v1/sources/${id}`, {method: "DELETE"});
  $("delete-dialog").close(); state.detailEpoch++; state.selected = null; state.document = null;
  $("source-detail").hidden = true; $("results").replaceChildren(); $("search-status").textContent = "Source deleted. Run search again to update evidence.";
  if ($("source-filter").value === id) $("source-filter").value = "";
  state.offset = 0; await loadSources(); message("notice", "Source deleted.");
}, "delete-status"));
$("search-form").addEventListener("submit", (event) => {
  event.preventDefault(); action(event.submitter, async () => {
    const payload = {query: $("query").value, limit: Number($("limit").value), mode: $("mode").value, debug: $("debug").checked,
      filters: {source_ids: $("source-filter").value ? [$("source-filter").value] : []}};
    $("results").replaceChildren(); $("search-status").textContent = "Searching…";
    try {
      const data = await api("/v1/search", {method: "POST", body: payload});
      $("search-status").textContent = data.results.length ? `${data.results.length} ranked results` : "No matching evidence. Sources must finish indexing before they can be searched.";
      for (const [rank, result] of data.results.entries()) {
        const item = node("article", undefined, "result");
        item.append(node("div", `#${rank + 1} · Score ${result.score.toFixed(4)} · ${result.format}`, "rank"), node("h3", result.title), node("p", result.heading_path.join(" › ") || "No section heading", "muted"));
        if (result.page_start) item.append(node("p", `Pages ${result.page_start}–${result.page_end}`, "small"));
        const uri = node("p", undefined, "reference"); reference(uri, result.uri); item.append(uri);
        item.append(node("p", result.text.slice(0, 240) + (result.text.length > 240 ? "…" : ""), "excerpt"));
        const details = node("details"); details.append(node("summary", "Full chunk text and provenance"), node("pre", result.text));
        details.append(node("p", `Source ${result.source_id}\nDocument ${result.document_id}\nChunk ${result.chunk_id}`, "reference"));
        if (result.source_uri) { const sourceUri = node("p", undefined, "reference"); reference(sourceUri, result.source_uri); details.append(sourceUri); }
        item.append(details);
        const inspect = node("button", "Inspect source"); inspect.addEventListener("click", () => action(inspect, () => selectSource(result.source_id))); item.append(inspect);
        if (result.score_components) { const debug = node("details"); debug.append(node("summary", "Score breakdown"), node("pre", JSON.stringify(result.score_components, null, 2))); item.append(debug); }
        $("results").append(item);
      }
    } catch (error) { $("search-status").textContent = error.name === "AbortError" ? "" : error.message; throw error; }
  });
});
// Sequential polling avoids overlapping reads, pauses in background tabs and retains expansion state.
async function poll() {
  try {
    if (state.token && !document.hidden && (state.sources.some(active) || (state.selected && active(state.selected)))) {
      await loadSources();
      if (state.selected) {
        const before = `${state.selected.status}:${state.selected.document_count}:${state.selected.chunk_count}`;
        await loadDetail(state.selected.id, state.detailEpoch);
        if (state.selected && before !== `${state.selected.status}:${state.selected.document_count}:${state.selected.chunk_count}`) {
          await loadDocuments(); if (state.document) await loadChunks();
        }
      }
    }
  } catch (error) { if (error.name !== "AbortError") message("notice", error.message, true); }
  finally { setTimeout(poll, 2500); }
}
setTimeout(poll, 2500);
