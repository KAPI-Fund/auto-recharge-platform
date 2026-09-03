"use client";

import { useLayoutEffect } from "react";

export function PublicThemeBody({ bodyClassName }: { bodyClassName: string }) {
  useLayoutEffect(() => {
    const classes = bodyClassName.split(/\s+/).filter(Boolean);
    classes.forEach((className) => document.body.classList.add(className));
    return () => classes.forEach((className) => document.body.classList.remove(className));
  }, [bodyClassName]);

  return null;
}
