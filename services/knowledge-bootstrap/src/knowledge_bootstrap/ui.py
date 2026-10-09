"""Public credential-free shell; all owner data/actions go through the bearer API."""

from pathlib import Path

from fastapi import Request
from fastapi.responses import HTMLResponse, RedirectResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates

ASSETS = Path(__file__).parent


def install_ui(app, settings):
    templates = Jinja2Templates(directory=ASSETS / "templates")
    app.mount(
        "/knowledge/static", StaticFiles(directory=ASSETS / "static"), name="knowledge-static"
    )

    @app.get("/", include_in_schema=False)
    def home():
        return RedirectResponse("/knowledge", status_code=307)

    @app.get("/knowledge", response_class=HTMLResponse, include_in_schema=False)
    def knowledge(request: Request):
        return templates.TemplateResponse(
            request=request,
            name="knowledge.html",
            context={
                "max_pages": settings.web_max_pages,
                "max_file_bytes": settings.max_file_bytes,
                "max_text_bytes": settings.max_input_bytes,
                "search_max_limit": settings.search_max_limit,
            },
        )

    @app.middleware("http")
    async def ui_headers(request: Request, call_next):
        response = await call_next(request)
        if request.url.path == "/knowledge" or request.url.path.startswith("/knowledge/static/"):
            response.headers["Content-Security-Policy"] = (
                "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; "
                "img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
            )
            response.headers["Referrer-Policy"] = "no-referrer"
            response.headers["X-Content-Type-Options"] = "nosniff"
            response.headers["Cache-Control"] = "no-store"
        if request.url.path.startswith("/v1/"):
            response.headers["Cache-Control"] = "no-store"
        return response
