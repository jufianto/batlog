"""Colours from brand/tokens.json, the one palette source. The builder holds no colour of its own.

The kit's files have always spelled hex in upper case, so the values are upper-cased here; that keeps every
output byte for byte the same as before the builder read tokens.
"""
import json
from pathlib import Path

TOKENS = Path(__file__).resolve().parents[2] / 'tokens.json'
_tokens = json.loads(TOKENS.read_text(encoding='utf-8'))['tokens']
USED = {}


def colour(path):
    group, name = path.split('.')
    value = _tokens[group][name]['value']
    USED[path] = value
    return value.upper()


INK = colour('brand.ink')
WHITE = colour('brand.white')
BLACK = colour('brand.black')
CYAN = colour('brand.cyan')
MIST = colour('guide.mist')
MUTED = colour('guide.muted')
SWATCH_EDGE = colour('guide.swatch-edge')
