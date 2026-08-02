<?php
session_start();
header('Content-Type: application/json');

// Set time limit for LLM actions
set_time_limit(300);
ini_set('max_execution_time', '300');

require_once __DIR__ . '/lib.php';

$action = $_GET['action'] ?? '';

// All content admin actions require authentication
$is_admin = !empty($_SESSION['admin_logged_in']);
if (!$is_admin) {
    http_response_code(401);
    echo json_encode(['status' => 'error', 'message' => 'Unauthorized. Please log in first.']);
    exit;
}

try {
    switch ($action) {
        case 'get_content_file':
            $file = $_GET['file'] ?? '';
            if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $file)) {
                throw new Exception("Invalid file name format.");
            }
            $path = __DIR__ . '/content/' . $file;
            if (!file_exists($path)) {
                echo json_encode(['status' => 'success', 'content' => '', 'mtime' => 0]);
            } else {
                echo json_encode([
                    'status' => 'success',
                    'content' => file_get_contents($path),
                    'mtime' => filemtime($path)
                ]);
            }
            break;

        case 'save_content_file':
            $file = $_POST['file'] ?? '';
            $content = $_POST['content'] ?? '';
            $expected_mtime = isset($_POST['expected_mtime']) ? (int)$_POST['expected_mtime'] : null;

            if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $file)) {
                throw new Exception("Invalid file name format.");
            }
            $path = __DIR__ . '/content/' . $file;

            // Conflict check if expected_mtime is provided
            if (file_exists($path) && $expected_mtime !== null && $expected_mtime > 0) {
                $actual_mtime = filemtime($path);
                if ($actual_mtime > $expected_mtime) {
                    http_response_code(409);
                    echo json_encode([
                        'status' => 'error',
                        'message' => 'Konflikt: Die Inhaltsdatei wurde auf dem Server zwischenzeitlich bearbeitet.',
                        'actual_mtime' => $actual_mtime,
                        'expected_mtime' => $expected_mtime
                    ]);
                    break;
                }
            }

            if (file_put_contents($path, $content) === false) {
                throw new Exception("Failed to write to file.");
            }
            clearstatcache(true, $path);
            echo json_encode([
                'status' => 'success',
                'message' => 'File saved successfully.',
                'mtime' => filemtime($path)
            ]);
            break;

        case 'suggest_meta':
            $name = trim($_POST['name'] ?? '');
            $language = trim($_POST['language'] ?? 'de');
            $title = trim($_POST['title'] ?? '');
            $content_file = trim($_POST['content_file'] ?? '');
            $html_teaser = trim($_POST['html_teaser'] ?? '');

            if (empty($name) || empty($title)) {
                throw new Exception("Name and title are required for suggestions.");
            }

            $content = '';
            if (!empty($content_file)) {
                if (!preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $content_file)) {
                    throw new Exception("Invalid content file format.");
                }
                $path = __DIR__ . '/content/' . $content_file;
                if (file_exists($path)) {
                    $content = file_get_contents($path);
                }
            }
            if (empty($content)) {
                $content = $html_teaser;
            }

            $plain_text = strip_tags($content);
            if (empty($plain_text)) {
                $plain_text = "No additional content provided.";
            }

            $system_prompt = "You are an AI assistant helping organize content tiles. You will be given a tile's name, title, and plain text content.
Analyze the content and generate:
1. A concise, high-level summary (Kurzfassung) of the tile, combining the title and main content, up to 400 words (500 tokens). This summary is strictly used for vector search.
2. 3 to 6 category tags representing the main themes of the tile.

Respond ONLY with a valid JSON object matching the following structure (no markdown formatting, no backticks, no extra text):
{
  \"summary\": \"The high-level summary here...\",
  \"tags\": [\"tag1\", \"tag2\", \"tag3\"]
}";

            $user_content = "Name: $name\nTitle: $title\nContent:\n$plain_text";

            $llm_response = call_llm($system_prompt, $user_content);
            $llm_response = trim($llm_response);
            if (strpos($llm_response, '```') !== false) {
                $llm_response = preg_replace('/^```(?:json)?|```$/m', '', $llm_response);
                $llm_response = trim($llm_response);
            }

            $parsed = json_decode($llm_response, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("LLM returned invalid JSON: " . $llm_response);
            }

            echo json_encode([
                'status' => 'success',
                'data' => $parsed
            ]);
            break;

        case 'edit_html_with_llm':
            $prompt = trim($_POST['prompt'] ?? '');
            $html_teaser = $_POST['html_teaser'] ?? '';

            if (empty($prompt)) {
                throw new Exception("Prompt instruction is required.");
            }

            $system_prompt = "You are an expert HTML editor assistant. Follow the user's instructions to modify or transform the provided HTML document. Respond ONLY with the modified HTML document. Do not include markdown code block formatting (no backticks like ```html), explanations, or any extra text.";
            $user_content = "Instruction: $prompt\n\nHTML Document:\n$html_teaser";

            $llm_response = call_llm($system_prompt, $user_content);
            $llm_response = trim($llm_response);
            if (strpos($llm_response, '```') !== false) {
                $llm_response = preg_replace('/^```(?:html|xml|json)?|```$/m', '', $llm_response);
                $llm_response = trim($llm_response);
            }

            echo json_encode([
                'status' => 'success',
                'html_teaser' => $llm_response
            ]);
            break;

        default:
            throw new Exception("Invalid content administration action.");
    }

} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'status' => 'error',
        'message' => $e->getMessage()
    ]);
}
