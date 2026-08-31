import * as React from "react"
import { cn } from "@/lib/utils"

export function Message({ from, className, ...props }: React.HTMLAttributes<HTMLDivElement> & { from: "user" | "assistant" }) {
  return <div data-from={from} className={cn("group flex w-full flex-col gap-2 data-[from=user]:items-end", className)} {...props} />
}
export function MessageContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("max-w-[92%] rounded-xl border border-border bg-secondary/60 px-3.5 py-3 text-sm leading-relaxed group-data-[from=user]:border-primary/20 group-data-[from=user]:bg-primary/10", className)} {...props} />
}
export function MessageResponse({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn("whitespace-pre-wrap", className)} {...props} />
}
