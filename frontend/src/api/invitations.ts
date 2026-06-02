import { api } from "./client";
import type {
  InvitationEmailSuppression,
  InvitationEmailSuppressionResponse,
  InvitationEmailSuppressionsResponse,
} from "../domain/invitation";

export async function listInvitationEmailSuppressions(
  organizationID: string,
): Promise<InvitationEmailSuppression[]> {
  const data = await api<InvitationEmailSuppressionsResponse>(
    `/api/organizations/${encodeURIComponent(organizationID)}/invitation-email-suppressions`,
  );
  return data.suppressions || [];
}

export async function deleteInvitationEmailSuppression(
  organizationID: string,
  suppressionID: string,
): Promise<InvitationEmailSuppression> {
  const data = await api<InvitationEmailSuppressionResponse>(
    `/api/organizations/${encodeURIComponent(
      organizationID,
    )}/invitation-email-suppressions/${encodeURIComponent(suppressionID)}`,
    { method: "DELETE" },
  );
  return data.suppression;
}
