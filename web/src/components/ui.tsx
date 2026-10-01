import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-full text-sm font-semibold transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-lime-300 disabled:pointer-events-none disabled:opacity-45",
  {
    variants: {
      variant: {
        default:
          "bg-lime-300 text-zinc-950 shadow-[0_0_24px_rgba(190,242,100,.16)] hover:bg-lime-200",
        outline:
          "border border-white/14 bg-white/5 text-white hover:border-white/25 hover:bg-white/10",
        ghost: "text-zinc-300 hover:bg-white/7 hover:text-white",
      },
      size: {
        default: "h-11 px-5",
        sm: "h-8 px-3 text-xs",
        icon: "size-10",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: ButtonProps) {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export function Card({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "rounded-[22px] border border-white/10 bg-zinc-900/65 shadow-[0_24px_80px_rgba(0,0,0,.32)] backdrop-blur-xl",
        className,
      )}
      {...props}
    />
  );
}

export function Badge({
  className,
  tone = "neutral",
  ...props
}: React.HTMLAttributes<HTMLSpanElement> & {
  tone?: "neutral" | "lime" | "blue" | "red" | "amber";
}) {
  const tones = {
    neutral: "border-white/10 bg-white/6 text-zinc-300",
    lime: "border-lime-300/25 bg-lime-300/10 text-lime-200",
    blue: "border-sky-300/25 bg-sky-300/10 text-sky-200",
    red: "border-rose-300/25 bg-rose-300/10 text-rose-200",
    amber: "border-amber-300/25 bg-amber-300/10 text-amber-200",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[10px] font-bold uppercase tracking-[.14em]",
        tones[tone],
        className,
      )}
      {...props}
    />
  );
}
