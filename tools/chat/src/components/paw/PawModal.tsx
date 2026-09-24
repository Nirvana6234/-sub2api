"use client";

import { useEffect, type ReactNode } from "react";
import { PawCloseIcon } from "./PawIcons";

interface PawModalProps {
  title: string;
  children: ReactNode;
  onClose: () => void;
  actions?: ReactNode;
}

export function PawModal({ title, children, onClose, actions }: PawModalProps) {
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <div className="paw-modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className="paw-modal"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="paw-modal-head">
          <h2>{title}</h2>
          <button type="button" className="paw-icon-button" onClick={onClose} aria-label="关闭">
            <PawCloseIcon width={16} height={16} />
          </button>
        </header>
        <div className="paw-modal-body">{children}</div>
        {actions ? <footer className="paw-modal-actions">{actions}</footer> : null}
      </section>
    </div>
  );
}
