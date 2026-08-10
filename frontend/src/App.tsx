import { backend } from "@/backend";
import { ServerAccessView } from "@/components/server/server-access-view";
import { Spinner } from "@/components/ui/spinner";
import type { BootstrapData } from "@/types";
import { useEffect, useState } from "react";

function App() {
  const [data, setData] = useState<BootstrapData>();
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    backend
      .bootstrap()
      .then((result) => {
        if (active) setData(result);
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => {
      active = false;
    };
  }, []);

  if (error) {
    return (
      <div className="grid min-h-screen place-items-center bg-background px-8 text-center text-sm text-destructive">
        {error}
      </div>
    );
  }
  if (!data) {
    return (
      <div className="grid min-h-screen place-items-center bg-background text-foreground">
        <Spinner />
      </div>
    );
  }
  return <ServerAccessView profiles={data.serverProfiles} migration={data.migration} />;
}

export default App;
