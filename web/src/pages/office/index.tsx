import { useParams } from "react-router";

import { useOfficeChat } from "./use-office-chat";
import { OfficeShell } from "./components/office-shell";

export default function OfficePage() {
    const { sessionId } = useParams();
    useOfficeChat(sessionId);
    return <OfficeShell />;
}
