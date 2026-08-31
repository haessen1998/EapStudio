import * as React from "react"
import { cn } from "@/lib/utils"

export function Conversation({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex min-h-0 flex-1 flex-col overflow-y-auto", className)} {...props} />
}
export function ConversationContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-col gap-4 p-4", className)} {...props} />
}
export function ConversationEmptyState({ icon, title, description }: { icon?: React.ReactNode; title: string; description: string }) {
  return <div className="m-auto flex max-w-64 flex-col items-center gap-2 px-6 py-10 text-center text-muted-foreground">{icon}<p className="text-sm font-medium text-foreground">{title}</p><p className="text-xs leading-relaxed">{description}</p></div>
}
