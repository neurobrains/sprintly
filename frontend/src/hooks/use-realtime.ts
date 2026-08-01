"use client";

import * as React from "react";
import { useQueryClient } from "@tanstack/react-query";

import { eventsUrl } from "@/lib/api";
import { supabase } from "@/lib/supabase/client";
import type { RealtimeEvent } from "@/lib/types";

type Status = "connecting" | "open" | "closed";

/**
 * Subscribes to a workspace's live event stream.
 *
 * Reconnects with exponential backoff, and re-reads the access token on each
 * attempt — a socket that died because the JWT expired must not retry with the
 * same dead token.
 */
export function useRealtime(
  workspace: string,
  onEvent?: (event: RealtimeEvent) => void,
) {
  const queryClient = useQueryClient();
  const [status, setStatus] = React.useState<Status>("connecting");
  const [online, setOnline] = React.useState<Set<string>>(new Set());

  // Kept in a ref so a changing callback never forces a reconnect.
  const handlerRef = React.useRef(onEvent);
  handlerRef.current = onEvent;

  // Exposed to `send` without making it a dependency of the connect effect.
  const socketRef = React.useRef<WebSocket | null>(null);

  React.useEffect(() => {
    if (!workspace) return;

    let socket: WebSocket | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let cancelled = false;

    async function connect() {
      if (cancelled) return;

      const {
        data: { session },
      } = await supabase().auth.getSession();

      if (!session || cancelled) return;

      setStatus("connecting");
      socket = new WebSocket(eventsUrl(workspace, session.access_token));
      socketRef.current = socket;

      socket.onopen = () => {
        attempt = 0;
        setStatus("open");
      };

      socket.onmessage = (message) => {
        let event: RealtimeEvent;
        try {
          event = JSON.parse(message.data);
        } catch {
          return;
        }

        applyEvent(event);
        handlerRef.current?.(event);
      };

      socket.onclose = () => {
        setStatus("closed");
        if (cancelled) return;

        // 1s, 2s, 4s … capped at 30s.
        const delay = Math.min(1000 * 2 ** attempt++, 30_000);
        retryTimer = setTimeout(connect, delay);
      };

      socket.onerror = () => socket?.close();
    }

    function applyEvent(event: RealtimeEvent) {
      switch (event.type) {
        case "presence.connected":
          if (event.actor_id) {
            setOnline((prev) => new Set(prev).add(event.actor_id!));
          }
          break;

        case "presence.disconnected":
          if (event.actor_id) {
            setOnline((prev) => {
              const next = new Set(prev);
              next.delete(event.actor_id!);
              return next;
            });
          }
          break;

        case "task.created":
        case "task.updated":
        case "task.moved":
        case "task.deleted":
          queryClient.invalidateQueries({ queryKey: ["tasks", workspace] });
          queryClient.invalidateQueries({ queryKey: ["projects", workspace] });
          break;

        case "comment.created":
          queryClient.invalidateQueries({ queryKey: ["comments", workspace] });
          queryClient.invalidateQueries({ queryKey: ["activity", workspace] });
          break;

        case "notification.new":
          queryClient.invalidateQueries({ queryKey: ["notifications", workspace] });
          break;

        case "member.joined":
        case "member.updated":
        case "member.removed":
          queryClient.invalidateQueries({ queryKey: ["members", workspace] });
          break;

        case "project.created":
        case "project.updated":
          queryClient.invalidateQueries({ queryKey: ["projects", workspace] });
          break;
      }
    }

    connect();

    return () => {
      cancelled = true;
      clearTimeout(retryTimer);
      socketRef.current = null;
      // Drop onclose first so teardown does not schedule a reconnect.
      if (socket) {
        socket.onclose = null;
        socket.close();
      }
    };
  }, [workspace, queryClient]);

  const send = React.useCallback((type: string, payload?: unknown) => {
    const socket = socketRef.current;
    // Dropped when the socket is down by design: presence and typing are
    // ephemeral, and queuing them would replay stale signals on reconnect.
    if (socket?.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type, payload }));
  }, []);

  return { status, online, send };
}
