from __future__ import annotations

import base64
import html
import random

CAPTCHA_ALPHABET = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"


def generate_captcha_code(length: int = 4) -> str:
    return "".join(random.choice(CAPTCHA_ALPHABET) for _ in range(length))


def render_captcha_data_uri(code: str) -> str:
    width, height = 168, 56
    seed = sum((index + 1) * ord(char) for index, char in enumerate(code))
    rng = random.Random(seed)

    circles = []
    for _ in range(7):
        circles.append(
            f'<circle cx="{rng.randint(8, width - 8)}" cy="{rng.randint(8, height - 8)}" '
            f'r="{rng.randint(2, 4)}" fill="rgba({rng.randint(180, 235)}, {rng.randint(190, 235)}, 255, 0.32)" />'
        )

    paths = []
    for _ in range(4):
        start_y = rng.randint(10, height - 10)
        control_y = rng.randint(4, height - 4)
        end_y = rng.randint(10, height - 10)
        paths.append(
            f'<path d="M {-8} {start_y} Q {width / 2:.1f} {control_y} {width + 8} {end_y}" '
            f'stroke="rgba({rng.randint(120, 190)}, {rng.randint(135, 205)}, {rng.randint(190, 245)}, 0.42)" '
            f'stroke-width="{rng.uniform(1.2, 2.1):.2f}" fill="none" stroke-linecap="round" />'
        )

    glyphs = []
    start_x = 24
    gap = 33
    for index, char in enumerate(code):
        x = start_x + index * gap + rng.randint(-2, 2)
        y = 36 + rng.randint(-4, 5)
        rotate = rng.randint(-12, 12)
        font_size = rng.randint(25, 31)
        glyphs.append(
            f'<text x="{x}" y="{y}" transform="rotate({rotate} {x} {y})" '
            f'fill="#0f172a" fill-opacity="{rng.uniform(0.84, 0.98):.2f}" '
            f'font-family="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace" '
            f'font-size="{font_size}" font-weight="700" letter-spacing="1.5">{html.escape(char)}</text>'
        )

    svg = f"""
<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}" role="img" aria-label="验证码">
  <defs>
    <linearGradient id="captcha-bg" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="#f8fbff" />
      <stop offset="100%" stop-color="#eef4ff" />
    </linearGradient>
    <filter id="soft-noise">
      <feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves="1" seed="{seed}" />
      <feColorMatrix type="saturate" values="0" />
      <feComponentTransfer>
        <feFuncA type="table" tableValues="0 0 0.08 0" />
      </feComponentTransfer>
    </filter>
  </defs>
  <rect x="0.75" y="0.75" width="{width - 1.5}" height="{height - 1.5}" rx="16" fill="url(#captcha-bg)" stroke="rgba(148,163,184,0.28)" />
  <rect x="0" y="0" width="{width}" height="{height}" rx="16" fill="white" filter="url(#soft-noise)" opacity="0.45" />
  {''.join(circles)}
  {''.join(paths)}
  {''.join(glyphs)}
</svg>
""".strip()
    encoded = base64.b64encode(svg.encode("utf-8")).decode("ascii")
    return f"data:image/svg+xml;base64,{encoded}"
