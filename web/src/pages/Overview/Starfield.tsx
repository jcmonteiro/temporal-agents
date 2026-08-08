import type { ReactNode } from "react";

// Deterministic pseudo-random starfield (same seed → same field), rendered as
// SVG for crispness. Kept intentionally sparse and CPU-cheap.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Props {
  width: number;
  height: number;
  count?: number;
  seed?: number;
}

export function Starfield({ width, height, count = 90, seed = 42 }: Props): ReactNode {
  const rand = mulberry32(seed);
  const stars = Array.from({ length: count }, () => ({
    x: rand() * width,
    y: rand() * height,
    r: rand() * 1.3 + 0.3,
  }));
  return (
    <g aria-hidden="true" fill="var(--star-color)">
      {stars.map((s, i) => (
        <circle key={i} cx={s.x} cy={s.y} r={s.r} />
      ))}
    </g>
  );
}
