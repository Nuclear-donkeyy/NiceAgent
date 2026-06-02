export interface InvitationEmailSuppression {
  id: string;
  organization_id: string;
  email: string;
  reason?: string;
  source_event_id?: string;
  provider?: string;
  provider_message_id?: string;
  created_at: string;
  updated_at: string;
}

export interface InvitationEmailSuppressionsResponse {
  suppressions: InvitationEmailSuppression[];
}

export interface InvitationEmailSuppressionResponse {
  suppression: InvitationEmailSuppression;
}
