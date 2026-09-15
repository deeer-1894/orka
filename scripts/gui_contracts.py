#!/usr/bin/env python3
"""Run GUI unittest classes and legacy plain test functions without pytest."""
import argparse
from contextlib import ExitStack
import os
from types import SimpleNamespace
from unittest.mock import patch
import importlib
import inspect
from pathlib import Path
import sys
import unittest


def function_contract(fn):
    def run():
        with ExitStack() as patches:
            parameters = inspect.signature(fn).parameters
            if set(parameters) - {'monkeypatch'}:
                raise RuntimeError(f'{fn.__name__} needs unsupported fixtures')
            fixtures = {}
            if 'monkeypatch' in parameters:
                fixtures['monkeypatch'] = SimpleNamespace(setenv=lambda key, value: patches.enter_context(patch.dict(os.environ, {key: str(value)})))
            fn(**fixtures)
    return run


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--gui-root', type=Path, default=Path(__file__).resolve().parent.parent / 'gui_agent')
    args = parser.parse_args()
    root = args.gui_root.resolve()
    tests = root / 'tests'
    sys.path[:0] = [str(root), str(tests)]
    suite = unittest.defaultTestLoader.discover(str(tests), pattern='test_*.py')
    for path in sorted(tests.glob('test_*.py')):
        module = importlib.import_module(path.stem)
        for name, fn in inspect.getmembers(module, inspect.isfunction):
            if name.startswith('test_') and fn.__module__ == module.__name__:
                suite.addTest(unittest.FunctionTestCase(function_contract(fn), description=f'{path.name}:{name}'))
    return 0 if unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful() else 1


if __name__ == '__main__':
    sys.exit(main())
