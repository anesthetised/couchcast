// JSON shapes returned by the REST API. Keep in sync with the Go response
// types in internal/apihttp.

export type Role = "user" | "admin";

export interface User {
  id: string;
  username: string;
  role: Role;
  createdAt: string;
}
