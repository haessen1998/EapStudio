import * as React from "react"
import { CheckCircle2 } from "lucide-react"
import { cn } from "@/lib/utils"

export function Tool({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-lg border border-border bg-background/50 p-3 text-xs", className)} {...props} />
}
export function ToolHeader({ title, status = "completed" }: { title: string; status?: string }) {
  return <div className="flex items-center gap-2 font-medium"><CheckCircle2 className="size-3.5 text-emerald-400" /><span>{title}</span><span className="ml-auto text-muted-foreground">{status}</span></div>
}
export function ToolContent({ children }: { children: React.ReactNode }) {
  return <div className="mt-2 text-muted-foreground">{children}</div>
}
