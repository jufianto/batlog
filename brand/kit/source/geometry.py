"""Hand-authored, normalized battery geometry. No raster tracing or masks."""
from tokens import INK

# One continuous silhouette, with the terminal integrated into the body.
OUTER = ('M168 109 H948 A92 92 0 0 1 1040 201 V285 H1082 '
         'A42 42 0 0 1 1124 327 V473 A42 42 0 0 1 1082 515 '
         'H1040 V599 A92 92 0 0 1 948 691 H168 '
         'A92 92 0 0 1 76 599 V201 A92 92 0 0 1 168 109 Z')


def channel(r=26):
    """Closed outline of the descending trace, including round caps/outer joins."""
    return (f'M205 {272-r} H440 A{r} {r} 0 0 1 {440+r} 272 '
            f'V{400-r} H678 A{r} {r} 0 0 1 {678+r} 400 '
            f'V{528-r} H912 A{r} {r} 0 0 1 912 {528+r} '
            f'H678 A{r} {r} 0 0 1 {678-r} 528 '
            f'V{400+r} H440 A{r} {r} 0 0 1 {440-r} 400 '
            f'V{272+r} H205 A{r} {r} 0 0 1 205 {272-r} Z')


def mark(color=INK, micro=False):
    return f'<path d="{OUTER} {channel(45 if micro else 26)}" fill="{color}" fill-rule="evenodd"/>'
