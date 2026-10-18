"""Render bounded workspace bytes without granting host filesystem access."""
import base64
import binascii

MAX_DOCUMENT = 1024 * 1024
MAX_ENCODED = ((MAX_DOCUMENT + 2) // 3) * 4
POLICY = ("default-src 'none'; script-src 'unsafe-inline' data:; "
          "style-src 'unsafe-inline' data:; img-src data:; font-src data:; "
          "connect-src 'none'; frame-src 'none'; object-src 'none'; "
          "form-action 'none'; base-uri 'none'")


def decode_document(encoded):
    if not isinstance(encoded, str) or not encoded or len(encoded) > MAX_ENCODED:
        raise ValueError("preview requires nonempty HTML up to 1 MiB")
    try:
        data = base64.b64decode(encoded, validate=True)
        if len(data) > MAX_DOCUMENT:
            raise ValueError("preview HTML exceeds 1 MiB")
        return data.decode("utf-8", errors="strict")
    except (binascii.Error, UnicodeError) as error:
        raise ValueError("preview requires base64-encoded UTF-8 HTML") from error


async def open_document(page, document):
    # A fresh data document cannot inherit the previous site's origin/storage.
    # The policy is parsed before any user bytes; later policies cannot relax it.
    await page.goto("data:text/html,<title>Orka workspace preview</title>", wait_until="load")
    prefix = '<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="' + POLICY + '">'
    await page.set_content(prefix + document, wait_until="load")
    return {}
