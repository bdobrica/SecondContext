"""Opt-in real-browser smoke test for a running service, using only its HTTP interface.

KNOWLEDGE_TOKEN=<configured token> uv run --with playwright python tests/ui_smoke.py
Requires Playwright Chromium, or --browser-path /usr/bin/chromium. Creates uniquely named
fixtures and deletes only their captured source IDs, including after a failed assertion.
Live indexing/search use the configured embedding provider (potentially paid).
"""

import argparse
import os
from pathlib import Path
from urllib.parse import urlsplit
from uuid import uuid4

import httpx
from playwright.sync_api import expect, sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8090")
    parser.add_argument("--browser-path")
    parser.add_argument("--website", default="https://example.com")
    parser.add_argument("--screenshot", type=Path)
    args = parser.parse_args()
    token = os.environ["KNOWLEDGE_TOKEN"]
    prefix = "K7 browser " + uuid4().hex[:12]
    created = []
    fixtures = Path(__file__).parent / "fixtures"
    errors = []

    with httpx.Client(
        base_url=args.url,
        headers={"Authorization": "Bearer " + token},
        trust_env=False,
        timeout=60,
    ) as api:
        try:
            with sync_playwright() as playwright:
                browser = playwright.chromium.launch(
                    executable_path=args.browser_path, headless=True, args=["--no-sandbox"]
                )
                context = browser.new_context(viewport={"width": 1440, "height": 1000})
                page = context.new_page()
                page.set_default_timeout(30000)
                page.on("pageerror", lambda error: errors.append(str(error)))

                def capture(response):
                    if (
                        response.request.method == "POST"
                        and urlsplit(response.url).path in {"/v1/sources", "/v1/sources/upload"}
                        and response.status == 202
                    ):
                        source = response.json()["source"]
                        if source["name"].startswith(prefix):
                            created.append(source["id"])

                page.on("response", capture)
                page.goto(args.url + "/")
                expect(page).to_have_url(args.url + "/knowledge")
                page.locator("#token").fill("invalid-browser-token")
                page.get_by_role("button", name="Connect", exact=True).click()
                expect(page.locator("#notice")).to_contain_text("unauthorized")
                expect(page.locator("#workspace")).to_be_hidden()
                page.locator("#token").fill(token)
                page.get_by_role("button", name="Connect", exact=True).click()
                expect(page.locator("#workspace")).to_be_visible()
                expect(page.locator("#sources-prev")).to_be_disabled()

                def add(mode):
                    page.locator("#add-source").click()
                    page.locator("#add-mode").select_option(mode)

                def submit(button, path="/v1/sources"):
                    with page.expect_response(
                        lambda r: urlsplit(r.url).path == path and r.request.method == "POST"
                    ) as response:
                        button.click()
                    result = response.value
                    assert result.status == 202, result.status
                    source_id = result.json()["source"]["id"]
                    expect(page.locator("#add-dialog")).not_to_be_visible()
                    expect(page.locator("#source-detail")).to_be_visible()
                    return source_id

                def ready():
                    expect(page.locator("#detail-summary")).to_contain_text(
                        "· ready ·", timeout=180000
                    )
                    expect(page.locator("#documents .document").first).to_be_visible()

                # Untrusted title and text are shown literally, never interpreted as HTML.
                add("paste")
                title = prefix + ' <img src=x onerror="window.__knowledge_xss=1">'
                page.locator("#paste-name").fill(title)
                page.locator("#paste-text").fill(
                    "# Rollback guide\n\nTo undo a deployment, restore the previous release.\n\n"
                    "Literal evidence: <script>window.__knowledge_xss=1</script>"
                )
                markdown_id = submit(page.locator("#paste-form button[type=submit]"))
                ready()
                expect(page.locator("#detail-title")).to_have_text(title)
                assert page.evaluate("window.__knowledge_xss") is None
                page.locator("#documents .document-name").first.click()
                expect(page.locator("#chunks .chunk").first).to_be_visible()
                page.locator("#chunks details").first.locator("summary").click()
                expect(page.locator("#chunks pre").first).to_contain_text("restore")
                expect(page.locator("#chunks-prev")).to_be_disabled()
                page.locator("#source-filter").select_option(markdown_id)
                page.locator("#query").fill("How do I undo a deployment?")
                page.locator("#debug").check()
                page.locator("#search-form button[type=submit]").click()
                expect(page.locator("#results .result").first).to_be_visible(timeout=60000)
                expect(page.locator("#results")).to_contain_text("Rollback guide")
                page.locator("#results .result").first.get_by_text(
                    "Score breakdown", exact=True
                ).click()
                expect(page.locator("#results")).to_contain_text("fusion")
                before = api.get(f"/v1/sources/{markdown_id}/documents").json()[0]["id"]
                for action in ("refresh", "reindex"):
                    page.locator(f"#{action}-source").click()
                    expect(page.locator("#notice")).to_contain_text("queued")
                    ready()
                    assert api.get(f"/v1/sources/{markdown_id}/documents").json()[0]["id"] == before

                # File selector/XHR progress path, including page and section provenance.
                for name in ("handbook.pdf", "handbook.docx"):
                    add("file")
                    page.locator("#file-name").fill(prefix + " " + name)
                    page.locator("#file").set_input_files(fixtures / "binary" / name)
                    submit(page.locator("#file-form button[type=submit]"), "/v1/sources/upload")
                    ready()
                    page.locator("#documents .document-name").first.click()
                    expect(page.locator("#chunks .chunk").first).to_be_visible()
                    expect(page.locator("#detail-summary")).to_contain_text(name.split(".")[1])
                    if name.endswith("pdf"):
                        expect(page.locator("#chunks")).to_contain_text("Pages")

                add("paste")
                page.locator("#paste-name").fill(prefix + " structured")
                page.locator("#paste-text").fill(
                    '{"runbook": {"rollback": "restore previous release"}}'
                )
                submit(page.locator("#paste-form button[type=submit]"))
                ready()
                expect(page.locator("#detail-summary")).to_contain_text("json")

                # Durable parser errors remain visible; retry queues a new job.
                add("paste")
                page.locator("#paste-name").fill(prefix + " failed")
                page.locator("#paste-text").fill("{broken JSON")
                page.locator("#paste-format").select_option("json")
                failed_id = submit(page.locator("#paste-form button[type=submit]"))
                expect(page.locator("#detail-error")).to_be_visible(timeout=60000)
                expect(page.locator("#detail-summary")).to_contain_text("failed")
                assert api.get("/v1/sources/" + failed_id).json()["last_error_code"]

                add("website")
                page.locator("#website-name").fill(prefix + " website")
                page.locator("#website-url").fill("http://127.0.0.1")
                page.locator("#website-form button[type=submit]").click()
                expect(page.locator("#add-status")).to_contain_text("invalid_request")
                page.locator("#website-url").fill(args.website)
                submit(page.locator("#website-form button[type=submit]"))
                ready()
                expect(page.locator("#detail-summary")).to_contain_text("html")

                # Confirmation does not delete on cancel; confirmed deletion removes evidence.
                page.locator("#delete-source").click()
                page.locator("#cancel-delete").click()
                expect(page.locator("#source-detail")).to_be_visible()
                page.locator("#delete-source").click()
                deleting_id = page.locator("#delete-dialog").get_attribute("data-source-id")
                delete_pattern = f"**/v1/sources/{deleting_id}"
                page.route(
                    delete_pattern,
                    lambda route: (
                        route.fulfill(
                            status=503,
                            content_type="application/json",
                            body=(
                                '{"error":{"code":"deletion_unavailable",'
                                '"detail":"Retry deletion"}}'
                            ),
                        )
                        if route.request.method == "DELETE"
                        else route.continue_()
                    ),
                )
                page.locator("#confirm-delete").click()
                expect(page.locator("#delete-status")).to_contain_text("deletion_unavailable")
                expect(page.locator("#delete-dialog")).to_be_visible()
                assert api.get("/v1/sources/" + deleting_id).status_code == 200
                page.unroute(delete_pattern)
                page.locator("#confirm-delete").click()
                expect(page.locator("#notice")).to_have_text("Source deleted.")
                expect(page.locator("#source-detail")).to_be_hidden()
                assert api.get("/v1/sources/" + created[-1]).status_code == 404

                # Responsive layout and authentication expiry must clear protected content.
                page.set_viewport_size({"width": 390, "height": 844})
                assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth")
                if args.screenshot:
                    page.screenshot(path=str(args.screenshot), full_page=True)
                assert page.evaluate("localStorage.length + sessionStorage.length") == 0
                assert not context.cookies()
                page.route(
                    "**/v1/sources?*",
                    lambda route: route.fulfill(
                        status=401,
                        content_type="application/json",
                        body='{"error":{"code":"unauthorized","detail":"Credential expired"}}',
                    ),
                )
                page.locator("#reload-sources").click()
                expect(page.locator("#workspace")).to_be_hidden()
                expect(page.locator("#login")).to_be_visible()
                expect(page.locator("#notice")).to_contain_text("unauthorized")
                assert page.locator("#source-list").inner_text() == ""
                page.unroute("**/v1/sources?*")
                page.locator("#token").fill(token)
                page.get_by_role("button", name="Connect", exact=True).click()
                expect(page.locator("#workspace")).to_be_visible()
                page.locator("#disconnect").click()
                expect(page.locator("#workspace")).to_be_hidden()
                assert page.locator("#results").inner_text() == ""
                assert page.locator("#source-list").inner_text() == ""
                page.reload()
                expect(page.locator("#login")).to_be_visible()
                assert page.locator("#token").input_value() == ""
                assert not errors, errors
                browser.close()
            print("Browser smoke passed: paste, structured text, PDF/DOCX, website, polling,")
            print(
                "inspection, hybrid/debug search, refresh/reindex, confirmation, auth and mobile."
            )
        finally:
            for source_id in set(created):
                api.delete("/v1/sources/" + source_id).raise_for_status()
            print(f"Cleaned {len(set(created))} browser fixture sources through the API.")


if __name__ == "__main__":
    main()
