"use client";

import type { ReactNode, SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement>;

function icon(children: ReactNode, props: IconProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      {children}
    </svg>
  );
}

export function PawPlusIcon(props: IconProps) {
  return icon(<path d="M12 5v14M5 12h14" />, props);
}

export function PawRefreshIcon(props: IconProps) {
  return icon(
    <>
      <path d="M20 12a8 8 0 0 0-14.5-4.5" />
      <path d="M5 4.5v4.75H9.75" />
      <path d="M4 12a8 8 0 0 0 14.5 4.5" />
      <path d="M19 19.5v-4.75h-4.75" />
    </>,
    props,
  );
}

export function PawSendIcon(props: IconProps) {
  return icon(<path d="M5 12 19 5l-4 14-4-6-6-1z" />, props);
}

export function PawArrowDownIcon(props: IconProps) {
  return icon(
    <>
      <path d="M12 4v15" />
      <path d="m6 13 6 6 6-6" />
    </>,
    props,
  );
}

export function PawStopIcon(props: IconProps) {
  return icon(<rect x="6" y="6" width="12" height="12" rx="2" />, props);
}

export function PawPaperclipIcon(props: IconProps) {
  return icon(
    <path d="M15 6.5 8.5 13a3 3 0 1 0 4.2 4.2l5.7-5.7a5 5 0 1 0-7.1-7.1l-6.4 6.4" />,
    props,
  );
}

export function PawImageIcon(props: IconProps) {
  return icon(
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <circle cx="8.5" cy="9" r="1.2" />
      <path d="m5.5 17 4.5-4 3.2 2.8 2.2-2.1 3.1 3.3" />
    </>,
    props,
  );
}

export function PawLogoutIcon(props: IconProps) {
  return icon(
    <>
      <path d="M10 17H6a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2h4" />
      <path d="m15 8 4 4-4 4" />
      <path d="M19 12H9" />
    </>,
    props,
  );
}

export function PawTrashIcon(props: IconProps) {
  return icon(
    <>
      <path d="M4 7h16" />
      <path d="M10 11v6M14 11v6" />
      <path d="M6 7l1 13h10l1-13" />
      <path d="M9 7V5h6v2" />
    </>,
    props,
  );
}

export function PawCopyIcon(props: IconProps) {
  return icon(
    <>
      <rect x="9" y="9" width="10" height="10" rx="2" />
      <path d="M7 15H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v1" />
    </>,
    props,
  );
}

export function PawPinIcon(props: IconProps) {
  return icon(
    <path d="m14 4 6 6-3 3v5l-2 2-2-7-4-4 5-5ZM9 15l-5 5" />,
    props,
  );
}

export function PawVolumeIcon(props: IconProps) {
  return icon(
    <>
      <path d="M5 10v4h3l4 3V7l-4 3H5Z" />
      <path d="M16 9.5a4 4 0 0 1 0 5M18.5 7a7.5 7.5 0 0 1 0 10" />
    </>,
    props,
  );
}

export function PawVolumeOffIcon(props: IconProps) {
  return icon(
    <>
      <path d="M5 10v4h3l4 3V7l-4 3H5Z" />
      <path d="m16 10 4 4M20 10l-4 4" />
    </>,
    props,
  );
}

export function PawBreakIcon(props: IconProps) {
  return icon(
    <>
      <path d="M5 5v5a7 7 0 0 0 14 0V5" />
      <path d="M3 5h4M17 5h4M12 5v5" />
    </>,
    props,
  );
}

export function PawEditIcon(props: IconProps) {
  return icon(
    <>
      <path d="M4 20h4l10-10a2.5 2.5 0 0 0-3.5-3.5L4.5 16.5z" />
      <path d="m13.5 6.5 4 4" />
    </>,
    props,
  );
}

export function PawMenuIcon(props: IconProps) {
  return icon(
    <>
      <path d="M4 7h16" />
      <path d="M4 12h16" />
      <path d="M4 17h16" />
    </>,
    props,
  );
}

export function PawCloseIcon(props: IconProps) {
  return icon(<path d="m6 6 12 12M18 6 6 18" />, props);
}

export function PawDragIcon(props: IconProps) {
  return icon(
    <>
      <path d="M9 7h.01M15 7h.01M9 12h.01M15 12h.01M9 17h.01M15 17h.01" />
    </>,
    props,
  );
}

export function PawCheckIcon(props: IconProps) {
  return icon(<path d="m5 12 4 4 10-10" />, props);
}

export function PawSettingsIcon(props: IconProps) {
  return icon(
    <>
      <path d="M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Z" />
      <path d="m19 13.2 1.3 1-.1 2-1.7 1-1.6-.7-1.2.7-.3 1.8-1.8.8-1.6-1-1.6 1-1.8-.8-.3-1.8-1.2-.7-1.6.7-1.7-1-.1-2 1.3-1-.1-1.4-1.3-1 .1-2 1.7-1 1.6.7 1.2-.7.3-1.8 1.8-.8 1.6 1 1.6-1 1.8.8.3 1.8 1.2.7 1.6-.7 1.7 1 .1 2-1.3 1 .1 1.4Z" />
    </>,
    props,
  );
}

export function PawDownloadIcon(props: IconProps) {
  return icon(
    <>
      <path d="M12 4v11" />
      <path d="m7.5 10.5 4.5 4.5 4.5-4.5" />
      <path d="M5 20h14" />
    </>,
    props,
  );
}

export function PawUploadIcon(props: IconProps) {
  return icon(
    <>
      <path d="M12 20V9" />
      <path d="m7.5 13.5 4.5-4.5 4.5 4.5" />
      <path d="M5 4h14" />
    </>,
    props,
  );
}

export function PawMaximizeIcon(props: IconProps) {
  return icon(
    <>
      <path d="M8 4H4v4M16 4h4v4M20 16v4h-4M4 16v4h4" />
    </>,
    props,
  );
}

export function PawMinimizeIcon(props: IconProps) {
  return icon(
    <>
      <path d="M4 9h6V3M20 9h-6V3M20 15h-6v6M4 15h6v6" />
    </>,
    props,
  );
}

export function PawPromptIcon(props: IconProps) {
  return icon(
    <>
      <path d="M5 4h14v13H8l-3 3V4Z" />
      <path d="M8 8h8M8 12h5" />
    </>,
    props,
  );
}

export function PawSearchIcon(props: IconProps) {
  return icon(
    <>
      <circle cx="10.5" cy="10.5" r="5.5" />
      <path d="m15 15 4.5 4.5" />
    </>,
    props,
  );
}

export function PawSunIcon(props: IconProps) {
  return icon(
    <>
      <circle cx="12" cy="12" r="3.5" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.65 17.65l1.42 1.42M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.65 6.35l1.42-1.42" />
    </>,
    props,
  );
}

export function PawMoonIcon(props: IconProps) {
  return icon(
    <path d="M20 15.4A8.5 8.5 0 0 1 8.6 4 8.5 8.5 0 1 0 20 15.4Z" />,
    props,
  );
}

export function PawKeyboardIcon(props: IconProps) {
  return icon(
    <>
      <rect x="3" y="6" width="18" height="12" rx="2" />
      <path d="M6 10h.01M9 10h.01M12 10h.01M15 10h.01M18 10h.01M7 14h10" />
    </>,
    props,
  );
}

export function PawLayersIcon(props: IconProps) {
  return icon(
    <>
      <path d="m12 4 8 4-8 4-8-4 8-4Z" />
      <path d="m4 12 8 4 8-4" />
      <path d="m4 16 8 4 8-4" />
    </>,
    props,
  );
}

export function PawRobotIcon(props: IconProps) {
  return icon(
    <>
      <rect x="5" y="7" width="14" height="11" rx="3" />
      <path d="M12 4v3M8.5 12h.01M15.5 12h.01M9 15h6" />
    </>,
    props,
  );
}

export function PawBrainIcon(props: IconProps) {
  return icon(
    <>
      <path d="M9.5 5.5a3 3 0 0 0-5 2.2A3.3 3.3 0 0 0 5.6 14a3 3 0 0 0 4.4 4.1V5.5Z" />
      <path d="M14.5 5.5a3 3 0 0 1 5 2.2 3.3 3.3 0 0 1-1.1 6.3 3 3 0 0 1-4.4 4.1V5.5Z" />
      <path d="M12 5v14M8 9h2M14 9h2M8 14h2M14 14h2" />
    </>,
    props,
  );
}

export function PawRulerIcon(props: IconProps) {
  return icon(
    <>
      <path d="m4 7 3-3 13 13-3 3L4 7Z" />
      <path d="m8 8 2-2M11 11l2-2M14 14l2-2M17 17l2-2" />
    </>,
    props,
  );
}
