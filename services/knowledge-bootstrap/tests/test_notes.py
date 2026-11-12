from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.ingestion import process_next

NOTE = "Alex: I prefer small API review requests.\nSam: I propose a Friday review."
FIXTURES = Path(__file__).parent / "fixtures" / "binary"


def test_personal_notes_parse_without_storage(settings):
    # Deliberately unreachable DB: personal parsing must not persist a reference copy.
    with TestClient(create_app(settings)) as client:
        fields = {"purpose": "personal", "name": "API review", "text": NOTE}
        assert client.post("/v1/notes", data=fields).status_code == 401
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        response = client.post("/v1/notes", data=fields)
        assert response.status_code == 200
        assert response.json() == {"purpose": "personal", "text": NOTE, "format": "text"}
        assert client.post("/v1/notes", data={**fields, "purpose": "both"}).status_code == 422
        assert (
            client.post("/v1/notes", data={**fields, "meeting_date": "yesterday"}).status_code
            == 422
        )
        assert client.post("/v1/notes", data={**fields, "text": "x" * 65537}).status_code == 422
        assert (
            client.post("/v1/notes", data=fields, files={"file": ("notes.txt", NOTE)}).status_code
            == 422
        )


@pytest.mark.parametrize("fmt", ["text", "markdown", "pdf", "docx"])
def test_personal_file_parsers(settings, fmt):
    data = (FIXTURES / f"handbook.{fmt}").read_bytes() if fmt in {"pdf", "docx"} else NOTE.encode()
    with TestClient(create_app(settings)) as client:
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        response = client.post(
            "/v1/notes",
            data={
                "purpose": "personal",
                "name": "Notes",
                "format": "markdown" if fmt == "markdown" else "text",
            },
            files={"file": (f"notes.{fmt}", data)},
        )
        assert response.status_code == 200, response.text
        assert response.json()["format"] == fmt
        assert (
            "Deployment" in response.json()["text"]
            if fmt in {"pdf", "docx"}
            else "Alex" in response.json()["text"]
        )


@pytest.mark.integration
@pytest.mark.parametrize("uploaded", [False, True])
def test_reference_notes_keep_existing_ingestion_and_owner_scope(
    client, sessions, db_settings, uploaded
):
    fields = {"purpose": "reference", "name": "API review", "meeting_date": "2026-11-10"}
    kwargs = {"files": {"file": ("notes.txt", NOTE)}} if uploaded else {}
    if not uploaded:
        fields["text"] = NOTE
    response = client.post("/v1/notes", data=fields, headers={"Idempotency-Key": "note1"}, **kwargs)
    assert response.status_code == 200
    source = response.json()["source_id"]
    repeat = client.post("/v1/notes", data=fields, headers={"Idempotency-Key": "note1"}, **kwargs)
    assert repeat.json()["source_id"] == source
    assert process_next(sessions, db_settings)
    documents = client.get(f"/v1/sources/{source}/documents").json()
    assert len(documents) == 1 and documents[0]["text_content"] == NOTE
    saved = client.get(f"/v1/sources/{source}").json()
    assert saved["metadata_json"]["meeting_date"] == "2026-11-10"
    assert (
        client.get(
            f"/v1/sources/{source}", headers={"Authorization": "Bearer test-owner-b-token-456"}
        ).status_code
        == 404
    )
