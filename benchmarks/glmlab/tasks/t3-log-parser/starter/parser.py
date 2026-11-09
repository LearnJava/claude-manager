"""Implement per the spec in task description (this file is a stub)."""


def parse_log(path):
    raise NotImplementedError


def summarize(entries):
    raise NotImplementedError


if __name__ == "__main__":
    import sys

    src, dst = sys.argv[1], sys.argv[2]
    entries = parse_log(src)
    result = summarize(entries)
    import json

    with open(dst, "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False, indent=2, sort_keys=True)
