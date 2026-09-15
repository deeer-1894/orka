"""Legacy macro API is disabled: no replay, loading, or disk persistence.

Instructions and action arguments can contain credentials. Re-enabling macros
requires a separate scoped, precondition-checked design; MACRO_ENABLE does not
bypass this boundary.
"""


class MacroStore:
    def __init__(self, path: str):
        pass

    def get(self, instruction: str):
        return None

    def put(self, instruction: str, actions: list[dict]):
        pass
