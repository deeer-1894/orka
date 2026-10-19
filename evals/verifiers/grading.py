"""Shared helpers for eval verifiers.

A verifier receives the directory holding the downloaded deliverables, records
every failed assertion with Grader.fail, and ends with Grader.finish. Helpers
only parse files; nothing here imports or runs model-written code.
"""
import ast
import json
import os
import re
import sys
import zipfile
from html import unescape


class Grader:
    def __init__(self, root):
        self.root = root
        self.fails = []

    def fail(self, msg):
        self.fails.append(msg)

    def path(self, name):
        return os.path.join(self.root, name)

    def read(self, name):
        """Text of a deliverable, or None (recording a failure) when missing."""
        if not os.path.exists(self.path(name)):
            self.fail(f"{name} missing")
            return None
        with open(self.path(name), encoding="utf-8", errors="replace") as f:
            return f.read()

    def json(self, name):
        text = self.read(name)
        if text is None:
            return None
        try:
            return json.loads(text)
        except ValueError as e:
            self.fail(f"{name} is not valid JSON: {e}")
            return None

    def expect_png(self, name):
        if not os.path.exists(self.path(name)):
            self.fail(f"{name} missing")
            return
        with open(self.path(name), "rb") as f:
            if not f.read(8).startswith(b"\x89PNG"):
                self.fail(f"{name} is not a PNG")

    def python_ast(self, name):
        src = self.read(name)
        if src is None:
            return None, None
        try:
            return src, ast.parse(src)
        except SyntaxError as e:
            self.fail(f"{name} does not parse: {e}")
            return src, None

    def expect_tests(self, name, minimum):
        """Counts test functions statically; returns the source for further checks."""
        src, tree = self.python_ast(name)
        if tree is not None:
            n = sum(isinstance(x, (ast.FunctionDef, ast.AsyncFunctionDef)) and x.name.startswith("test") for x in ast.walk(tree))
            if n < minimum:
                self.fail(f"{name} defines {n} tests, want >= {minimum}")
        return src

    def xlsx_rows(self, name):
        """Cell text of the first worksheet, resolving shared and inline strings."""
        try:
            z = zipfile.ZipFile(self.path(name))
        except (OSError, zipfile.BadZipFile) as e:
            self.fail(f"{name} is not a valid xlsx: {e}")
            return []
        shared = []
        if "xl/sharedStrings.xml" in z.namelist():
            xml = z.read("xl/sharedStrings.xml").decode()
            shared = [unescape(re.sub(r"<[^>]+>", "", si)) for si in re.findall(r"<si>(.*?)</si>", xml, re.S)]
        sheet = next((n for n in sorted(z.namelist()) if n.startswith("xl/worksheets/sheet")), None)
        if not sheet:
            self.fail(f"{name} has no worksheet")
            return []
        rows = []
        for row in re.findall(r"<row[^>]*>(.*?)</row>", z.read(sheet).decode(), re.S):
            cells = []
            for attrs, body in re.findall(r"<c([^>]*)>(.*?)</c>", row, re.S):
                text = unescape(re.sub(r"<[^>]+>", "", body)).strip()
                if 't="s"' in attrs and text.isdigit() and int(text) < len(shared):
                    text = shared[int(text)]
                cells.append(text)
            rows.append(cells)
        return rows

    def docx_text(self, name):
        try:
            xml = zipfile.ZipFile(self.path(name)).read("word/document.xml").decode()
        except (OSError, KeyError, zipfile.BadZipFile) as e:
            self.fail(f"{name} is not a valid docx: {e}")
            return ""
        return re.sub(r"<[^>]+>", "", xml)

    def finish(self):
        for f in self.fails:
            print("FAIL:", f)
        sys.exit(1 if self.fails else 0)


def links(text):
    return set(re.findall(r"https?://[^\s)>\]|]+", text))
