import type { ReactNode } from "react";
import type { IconName } from "../domain/work-item";

// Minimal inline SVG icon set. A slice using a public icon library (Lucide)
// can swap this out without changing callers.
interface Props {
  name: IconName;
  size?: number;
  color?: string;
}

export function Icon({ name, size = 20, color = "currentColor" }: Props): ReactNode {
  const common = {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: color,
    strokeWidth: 1.75,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
  };
  switch (name) {
    case "rocket":
      return (
        <svg {...common}>
          <path d="M14 3c3 0 7 4 7 7-2 0-4 1-5 2l-5 5-3-3 5-5c1-1 2-3 2-5l-1-1z" />
          <path d="M6 18l-3 3 3-3z" />
          <circle cx="15" cy="9" r="1.3" />
        </svg>
      );
    case "users":
      return (
        <svg {...common}>
          <circle cx="9" cy="8" r="3.2" />
          <path d="M2.5 20c.6-3.4 3.3-5.5 6.5-5.5s5.9 2.1 6.5 5.5" />
          <circle cx="17" cy="7" r="2.6" />
          <path d="M15.5 14c2.4.3 4.4 2 5 5" />
        </svg>
      );
    case "document":
      return (
        <svg {...common}>
          <path d="M7 3h7l4 4v14H7z" />
          <path d="M14 3v4h4" />
          <path d="M9 12h6M9 16h6" />
        </svg>
      );
    case "clock":
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="8.5" />
          <path d="M12 7.5V12l3 2" />
        </svg>
      );
    case "check":
      return (
        <svg {...common}>
          <circle cx="12" cy="12" r="8.5" />
          <path d="M8.5 12l2.5 2.5L15.5 10" />
        </svg>
      );
    case "alert":
      return (
        <svg {...common}>
          <path d="M12 3l9.5 17H2.5z" />
          <path d="M12 10v4" />
          <circle cx="12" cy="17.5" r="0.6" fill={color} stroke="none" />
        </svg>
      );
  }
}
