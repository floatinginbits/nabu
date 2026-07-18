import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { client } from "./client";
import type { components } from "./schema";

type CreateTaskRequest = components["schemas"]["CreateTaskRequest"];

export function useTasks() {
  return useQuery({
    queryKey: ["tasks"],
    queryFn: async () => {
      const { data, error } = await client.GET("/api/v1/tasks");
      if (error) throw new Error(error.error.message);
      return data;
    },
  });
}

export function useProjects() {
  return useQuery({
    queryKey: ["projects"],
    queryFn: async () => {
      const { data, error } = await client.GET("/api/v1/projects");
      if (error) throw new Error(error.error.message);
      return data;
    },
  });
}

export function useCreateTask() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: CreateTaskRequest) => {
      const { data, error } = await client.POST("/api/v1/tasks", { body });
      if (error) throw new Error(error.error.message);
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["tasks"] }),
  });
}
