import { WorkspaceProvider } from "@/components/workspace/workspace-provider";
import { Sidebar } from "@/components/workspace/sidebar";

export default async function WorkspaceLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;

  return (
    <WorkspaceProvider slug={slug}>
      <div className="flex h-dvh overflow-hidden">
        <Sidebar />
        <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      </div>
    </WorkspaceProvider>
  );
}
